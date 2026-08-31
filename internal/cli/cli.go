package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"gopkg.in/yaml.v3"

	"github.com/QuantaStream/qstream-migrate/internal/analyze"
	checkpkg "github.com/QuantaStream/qstream-migrate/internal/check"
	"github.com/QuantaStream/qstream-migrate/internal/exportjsonl"
	"github.com/QuantaStream/qstream-migrate/internal/generate"
	"github.com/QuantaStream/qstream-migrate/internal/loadplan"
	"github.com/QuantaStream/qstream-migrate/internal/model"
	"github.com/QuantaStream/qstream-migrate/internal/mysqlsource"
	"github.com/QuantaStream/qstream-migrate/internal/output"
	"github.com/QuantaStream/qstream-migrate/internal/postjsonl"
	"github.com/QuantaStream/qstream-migrate/internal/schemacmp"
)

func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "analyze":
		return runAnalyze(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "compare-schema":
		return runCompareSchema(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "generate":
		return runGenerate(args[1:], stdout, stderr)
	case "load-plan":
		return runLoadPlan(args[1:], stdout, stderr)
	case "post-jsonl":
		return runPostJSONL(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  qstream-migrate analyze mysql --dsn DSN [flags]
  qstream-migrate check --plan migration-plan/plan.yaml [flags]
  qstream-migrate compare-schema --generated configuration --reference reference-config [flags]
  qstream-migrate export mysql --dsn DSN --plan migration-plan/plan.yaml --out exports [flags]
  qstream-migrate generate --plan migration-plan/plan.yaml --out configuration [flags]
  qstream-migrate load-plan --plan migration-plan/plan.yaml --out migration-load [flags]
  qstream-migrate post-jsonl --input exports --target http://127.0.0.1:8088/ingest/json [flags]

Commands:
  analyze mysql   Inspect a MySQL schema and produce an editable migration plan.
  check           Validate that a migration plan is ready to generate or load.
  compare-schema  Compare generated QuantaStream schemas against a reference config.
  export mysql    Export MySQL rows as QuantaStream JSONL events.
  generate        Generate QuantaStream schema YAML from an editable plan.
  load-plan       Generate MySQL export and QuantaStream loader runbook files.
  post-jsonl      Post exported JSONL files to the QuantaStream loader.

Run "qstream-migrate <command> --help" for command flags.`)
}

func runAnalyze(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "analyze requires a source type; currently supported: mysql")
		return 2
	}
	switch args[0] {
	case "mysql":
		return runAnalyzeMySQL(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unsupported analyze source %q; currently supported: mysql\n", args[0])
		return 2
	}
}

func runAnalyzeMySQL(args []string, stdout, stderr io.Writer) int {
	var (
		dsn                    string
		schema                 string
		tableCSV               string
		outDir                 string
		profile                bool
		sampleLimit            int
		queryTimeout           time.Duration
		enumMaxDistinct        int64
		defaultLexPrefixLength int
	)

	fs := flag.NewFlagSet("qstream-migrate analyze mysql", flag.ContinueOnError)
	if hasHelpFlag(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.StringVar(&dsn, "dsn", "", "MySQL DSN, for example user:pass@tcp(127.0.0.1:3306)/dbname")
	fs.StringVar(&schema, "schema", "", "MySQL schema/database name; defaults to the database in --dsn")
	fs.StringVar(&tableCSV, "tables", "", "Optional comma-separated list of tables to analyze")
	fs.StringVar(&outDir, "out", "migration-plan", "Output directory")
	fs.BoolVar(&profile, "profile", true, "Collect lightweight column profiles")
	fs.IntVar(&sampleLimit, "sample-limit", 10, "Sample values per column; 0 disables value samples")
	fs.DurationVar(&queryTimeout, "query-timeout", 30*time.Second, "Timeout for each profiling query")
	fs.Int64Var(&enumMaxDistinct, "enum-max-distinct", 500, "Maximum distinct string values to recommend StringEnum")
	fs.IntVar(&defaultLexPrefixLength, "lex-prefix-length", 16, "Default StringLexBSI prefix length recommendation")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if dsn == "" {
		fmt.Fprintln(stderr, "--dsn is required")
		return 2
	}
	if schema == "" {
		schema = schemaFromDSN(dsn)
	}
	if schema == "" {
		fmt.Fprintln(stderr, "--schema is required when the DSN does not include a database name")
		return 2
	}

	ctx := context.Background()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		fmt.Fprintf(stderr, "open MySQL connection: %v\n", err)
		return 1
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = db.PingContext(pingCtx)
	cancel()
	if err != nil {
		fmt.Fprintf(stderr, "connect to MySQL: %v\n", err)
		return 1
	}

	tables := parseCSV(tableCSV)
	inv, err := mysqlsource.Introspect(ctx, db, mysqlsource.InspectOptions{
		Schema: schema,
		Tables: tables,
	})
	if err != nil {
		fmt.Fprintf(stderr, "introspect MySQL: %v\n", err)
		return 1
	}
	inv.Source = model.SourceInfo{
		Kind:      "mysql",
		Schema:    schema,
		DSNMasked: maskDSN(dsn),
	}

	mysqlsource.AddProfiles(ctx, db, &inv, mysqlsource.ProfileOptions{
		Enabled:      profile,
		SampleLimit:  sampleLimit,
		QueryTimeout: queryTimeout,
	})

	plan := analyze.BuildPlan(inv, analyze.Options{
		StringEnumMaxDistinct:  enumMaxDistinct,
		DefaultLexPrefixLength: defaultLexPrefixLength,
	})
	paths, err := output.WriteAnalysis(outDir, inv, plan)
	if err != nil {
		fmt.Fprintf(stderr, "write analysis: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "wrote %s\n", paths.Inventory)
	fmt.Fprintf(stdout, "wrote %s\n", paths.Plan)
	fmt.Fprintf(stdout, "wrote %s\n", paths.Readme)
	return 0
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	var (
		planPath         string
		relationshipMode string
		strict           bool
	)

	fs := flag.NewFlagSet("qstream-migrate check", flag.ContinueOnError)
	if hasHelpFlag(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.StringVar(&planPath, "plan", "", "Path to qstream-migrate plan.yaml")
	fs.StringVar(&relationshipMode, "relationship-mode", "metadata", "Relationship generation mode to validate: metadata, all, or none")
	fs.BoolVar(&strict, "strict", false, "Fail when warnings are present")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if planPath == "" {
		fmt.Fprintln(stderr, "--plan is required")
		return 2
	}
	if !isRelationshipMode(relationshipMode) {
		fmt.Fprintf(stderr, "unsupported relationship mode %q; use metadata, all, or none\n", relationshipMode)
		return 2
	}

	plan, err := readPlan(planPath)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	result := checkpkg.CheckPlan(plan, checkpkg.Options{
		RelationshipMode: relationshipMode,
		Strict:           strict,
	})
	for _, line := range checkpkg.FormatIssues(result) {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "plan_check result=%s errors=%d warnings=%d tables=%d fields=%d\n",
		result.Status(strict), result.Errors, result.Warnings, result.Tables, result.Fields)
	if !result.Pass(strict) {
		return 1
	}
	return 0
}

func runCompareSchema(args []string, stdout, stderr io.Writer) int {
	var (
		generatedDir string
		referenceDir string
		strict       bool
	)

	fs := flag.NewFlagSet("qstream-migrate compare-schema", flag.ContinueOnError)
	if hasHelpFlag(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.StringVar(&generatedDir, "generated", "", "Generated QuantaStream configuration directory")
	fs.StringVar(&referenceDir, "reference", "", "Reference QuantaStream configuration directory")
	fs.BoolVar(&strict, "strict", false, "Fail when differences are present")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if generatedDir == "" {
		fmt.Fprintln(stderr, "--generated is required")
		return 2
	}
	if referenceDir == "" {
		fmt.Fprintln(stderr, "--reference is required")
		return 2
	}

	result, err := schemacmp.CompareDirs(referenceDir, generatedDir)
	if err != nil {
		fmt.Fprintf(stderr, "compare schema: %v\n", err)
		return 1
	}
	for _, line := range schemacmp.FormatDifferences(result) {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "schema_compare result=%s differences=%d tables=%d\n",
		result.Status(), len(result.Differences), result.TablesCompared)
	if strict && !result.Match() {
		return 1
	}
	return 0
}

func runExport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "export requires a source type; currently supported: mysql")
		return 2
	}
	switch args[0] {
	case "mysql":
		return runExportMySQL(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unsupported export source %q; currently supported: mysql\n", args[0])
		return 2
	}
}

func runExportMySQL(args []string, stdout, stderr io.Writer) int {
	var (
		dsn              string
		planPath         string
		outDir           string
		tableCSV         string
		relationshipMode string
		queryTimeout     time.Duration
		overwrite        bool
	)

	fs := flag.NewFlagSet("qstream-migrate export mysql", flag.ContinueOnError)
	if hasHelpFlag(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.StringVar(&dsn, "dsn", "", "MySQL DSN, for example user:pass@tcp(127.0.0.1:3306)/dbname")
	fs.StringVar(&planPath, "plan", "", "Path to qstream-migrate plan.yaml")
	fs.StringVar(&outDir, "out", "exports", "Output directory for JSONL files")
	fs.StringVar(&tableCSV, "tables", "", "Optional comma-separated list of tables to export")
	fs.StringVar(&relationshipMode, "relationship-mode", "metadata", "Relationship generation mode to use for export ordering: metadata, all, or none")
	fs.DurationVar(&queryTimeout, "query-timeout", 0, "Optional timeout per table export query; 0 disables the timeout")
	fs.BoolVar(&overwrite, "overwrite", true, "Overwrite existing JSONL export files")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if dsn == "" {
		fmt.Fprintln(stderr, "--dsn is required")
		return 2
	}
	if planPath == "" {
		fmt.Fprintln(stderr, "--plan is required")
		return 2
	}
	if !isRelationshipMode(relationshipMode) {
		fmt.Fprintf(stderr, "unsupported relationship mode %q; use metadata, all, or none\n", relationshipMode)
		return 2
	}

	plan, err := readPlan(planPath)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		fmt.Fprintf(stderr, "open MySQL connection: %v\n", err)
		return 1
	}
	defer db.Close()

	ctx := context.Background()
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = db.PingContext(pingCtx)
	cancel()
	if err != nil {
		fmt.Fprintf(stderr, "connect to MySQL: %v\n", err)
		return 1
	}

	result, err := exportjsonl.ExportMySQL(ctx, db, plan, exportjsonl.Options{
		OutDir:           outDir,
		RelationshipMode: relationshipMode,
		Tables:           parseCSV(tableCSV),
		QueryTimeout:     queryTimeout,
		Overwrite:        overwrite,
	})
	if err != nil {
		fmt.Fprintf(stderr, "export mysql: %v\n", err)
		return 1
	}
	for _, table := range result.Tables {
		fmt.Fprintf(stdout, "exported table=%s rows=%d path=%s\n", table.Table, table.Rows, table.Path)
	}
	fmt.Fprintf(stdout, "export_mysql tables=%d out=%s\n", len(result.Tables), outDir)
	return 0
}

func runGenerate(args []string, stdout, stderr io.Writer) int {
	var (
		planPath             string
		outDir               string
		relationshipMode     string
		sourceRoot           string
		timestampGranularity string
		defaultLexLength     int
		overwrite            bool
	)

	fs := flag.NewFlagSet("qstream-migrate generate", flag.ContinueOnError)
	if hasHelpFlag(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.StringVar(&planPath, "plan", "", "Path to qstream-migrate plan.yaml")
	fs.StringVar(&outDir, "out", "configuration", "Output directory for QuantaStream schema files")
	fs.StringVar(&relationshipMode, "relationship-mode", "metadata", "Relationship generation mode: metadata, all, or none")
	fs.StringVar(&sourceRoot, "source-root", "/data", "JSON source path root for generated sourceName values")
	fs.StringVar(&timestampGranularity, "timestamp-granularity", "second", "TimestampBSI granularity")
	fs.IntVar(&defaultLexLength, "lex-prefix-length", 0, "Fallback StringLexBSI prefix length; defaults to plan settings")
	fs.BoolVar(&overwrite, "overwrite", true, "Overwrite existing generated schema files")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if planPath == "" {
		fmt.Fprintln(stderr, "--plan is required")
		return 2
	}
	if !isRelationshipMode(relationshipMode) {
		fmt.Fprintf(stderr, "unsupported relationship mode %q; use metadata, all, or none\n", relationshipMode)
		return 2
	}

	plan, err := readPlan(planPath)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	result, err := generate.WriteSchemas(plan, generate.Options{
		OutDir:               outDir,
		RelationshipMode:     relationshipMode,
		SourceRoot:           sourceRoot,
		TimestampGranularity: timestampGranularity,
		DefaultLexLength:     defaultLexLength,
		Overwrite:            overwrite,
	})
	if err != nil {
		fmt.Fprintf(stderr, "generate schemas: %v\n", err)
		return 1
	}
	for _, path := range result.Files {
		fmt.Fprintf(stdout, "wrote %s\n", path)
	}
	return 0
}

func runLoadPlan(args []string, stdout, stderr io.Writer) int {
	var (
		planPath         string
		outDir           string
		relationshipMode string
		loaderTarget     string
		batchSize        int
		overwrite        bool
	)

	fs := flag.NewFlagSet("qstream-migrate load-plan", flag.ContinueOnError)
	if hasHelpFlag(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.StringVar(&planPath, "plan", "", "Path to qstream-migrate plan.yaml")
	fs.StringVar(&outDir, "out", "migration-load", "Output directory for load runbook files")
	fs.StringVar(&relationshipMode, "relationship-mode", "metadata", "Relationship generation mode to use for parent-before-child ordering: metadata, all, or none")
	fs.StringVar(&loaderTarget, "loader-target", "http://127.0.0.1:8088/ingest/json", "QuantaStream loader JSON ingest URL")
	fs.IntVar(&batchSize, "batch-size", 2000, "Rows per JSON ingest request in the generated posting script")
	fs.BoolVar(&overwrite, "overwrite", true, "Overwrite existing generated load-plan files")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if planPath == "" {
		fmt.Fprintln(stderr, "--plan is required")
		return 2
	}
	if !isRelationshipMode(relationshipMode) {
		fmt.Fprintf(stderr, "unsupported relationship mode %q; use metadata, all, or none\n", relationshipMode)
		return 2
	}

	plan, err := readPlan(planPath)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	result, err := loadplan.Write(plan, loadplan.Options{
		OutDir:           outDir,
		RelationshipMode: relationshipMode,
		LoaderTarget:     loaderTarget,
		BatchSize:        batchSize,
		Overwrite:        overwrite,
	})
	if err != nil {
		fmt.Fprintf(stderr, "write load plan: %v\n", err)
		return 1
	}
	for _, path := range result.Files {
		fmt.Fprintf(stdout, "wrote %s\n", path)
	}
	fmt.Fprintf(stdout, "load_plan tables=%d out=%s\n", len(result.LoadOrder), outDir)
	return 0
}

func runPostJSONL(args []string, stdout, stderr io.Writer) int {
	var (
		inputDir  string
		targetURL string
		batchSize int
		commitURL string
		commit    bool
	)

	fs := flag.NewFlagSet("qstream-migrate post-jsonl", flag.ContinueOnError)
	if hasHelpFlag(args) {
		fs.SetOutput(stdout)
	} else {
		fs.SetOutput(stderr)
	}
	fs.StringVar(&inputDir, "input", "exports", "Input directory containing JSONL files and optional load-order.txt")
	fs.StringVar(&targetURL, "target", "http://127.0.0.1:8088/ingest/json", "QuantaStream loader JSON ingest URL")
	fs.IntVar(&batchSize, "batch-size", 2000, "Rows per JSON ingest request")
	fs.StringVar(&commitURL, "commit-url", "", "Optional QuantaStream loader commit URL; derived from --target when possible")
	fs.BoolVar(&commit, "commit", true, "POST to the loader commit endpoint after all rows are accepted")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if inputDir == "" {
		fmt.Fprintln(stderr, "--input is required")
		return 2
	}
	if targetURL == "" {
		fmt.Fprintln(stderr, "--target is required")
		return 2
	}

	result, err := postjsonl.PostDir(context.Background(), postjsonl.Options{
		InputDir:  inputDir,
		TargetURL: targetURL,
		BatchSize: batchSize,
		CommitURL: commitURL,
		Commit:    commit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "post jsonl: %v\n", err)
		return 1
	}
	for _, table := range result.Tables {
		fmt.Fprintf(stdout, "posted table=%s sent=%d accepted=%d failed=%d batches=%d path=%s\n",
			table.Table, table.Sent, table.Accepted, table.Failed, table.Batches, table.Path)
	}
	fmt.Fprintf(stdout, "post_jsonl sent=%d accepted=%d failed=%d batches=%d committed=%t\n",
		result.Sent, result.Accepted, result.Failed, result.Batches, result.Committed)
	return 0
}

func readPlan(planPath string) (model.MigrationPlan, error) {
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		return model.MigrationPlan{}, fmt.Errorf("read plan: %w", err)
	}
	var plan model.MigrationPlan
	if err := yaml.Unmarshal(planBytes, &plan); err != nil {
		return model.MigrationPlan{}, fmt.Errorf("parse plan: %w", err)
	}
	return plan, nil
}

func isRelationshipMode(mode string) bool {
	switch mode {
	case "metadata", "all", "none":
		return true
	default:
		return false
	}
}

func schemaFromDSN(dsn string) string {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return ""
	}
	return cfg.DBName
}

func maskDSN(dsn string) string {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "<unparsed>"
	}
	user := cfg.User
	if user == "" {
		user = "<user>"
	}
	netAddr := cfg.Addr
	if cfg.Net != "" {
		netAddr = cfg.Net + "(" + cfg.Addr + ")"
	}
	if netAddr == "" {
		netAddr = "<default>"
	}
	return fmt.Sprintf("%s:xxxxx@%s/%s", user, netAddr, cfg.DBName)
}

func parseCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}
