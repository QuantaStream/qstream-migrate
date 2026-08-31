package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/QuantaStream/qstream-migrate/internal/analyze"
	"github.com/QuantaStream/qstream-migrate/internal/model"
	"github.com/QuantaStream/qstream-migrate/internal/mysqlsource"
	"github.com/QuantaStream/qstream-migrate/internal/output"
)

func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "analyze":
		return runAnalyze(args[1:], stdout, stderr)
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

Commands:
  analyze mysql   Inspect a MySQL schema and produce an editable migration plan.

Run "qstream-migrate analyze mysql --help" for command flags.`)
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
