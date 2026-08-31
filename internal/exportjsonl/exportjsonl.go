package exportjsonl

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantaStream/qstream-migrate/internal/loadplan"
	"github.com/QuantaStream/qstream-migrate/internal/model"
)

type Options struct {
	OutDir           string
	RelationshipMode string
	Tables           []string
	QueryTimeout     time.Duration
	Overwrite        bool
}

type TableResult struct {
	Table string
	Path  string
	Rows  int64
}

type Result struct {
	Tables []TableResult
}

func ExportMySQL(ctx context.Context, db *sql.DB, plan model.MigrationPlan, opts Options) (Result, error) {
	opts = normalizeOptions(opts)
	filtered := filterPlan(plan, opts.Tables)
	order, err := loadplan.LoadOrder(filtered, opts.RelationshipMode)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create output directory: %w", err)
	}
	if err := writeText(filepath.Join(opts.OutDir, "load-order.txt"), strings.Join(order, "\n")+"\n", 0o644, opts.Overwrite); err != nil {
		return Result{}, err
	}

	tables := includedTablesByName(filtered)
	var result Result
	for _, tableName := range order {
		table := tables[tableName]
		tableResult, err := exportTable(ctx, db, filtered, table, opts)
		if err != nil {
			return Result{}, err
		}
		result.Tables = append(result.Tables, tableResult)
	}
	return result, nil
}

func normalizeOptions(opts Options) Options {
	if opts.OutDir == "" {
		opts.OutDir = "exports"
	}
	if opts.RelationshipMode == "" {
		opts.RelationshipMode = "metadata"
	}
	return opts
}

func exportTable(ctx context.Context, db *sql.DB, plan model.MigrationPlan, table model.TablePlan, opts Options) (TableResult, error) {
	fields := includedFields(table)
	if len(fields) == 0 {
		return TableResult{}, fmt.Errorf("table %s has no included fields", table.Name)
	}
	query := selectSQL(plan, table, fields)
	queryCtx := ctx
	cancel := func() {}
	if opts.QueryTimeout > 0 {
		queryCtx, cancel = context.WithTimeout(ctx, opts.QueryTimeout)
	}
	defer cancel()

	rows, err := db.QueryContext(queryCtx, query)
	if err != nil {
		return TableResult{}, fmt.Errorf("query table %s: %w", table.Name, err)
	}
	defer rows.Close()

	path := filepath.Join(opts.OutDir, table.Name+".jsonl")
	if !opts.Overwrite {
		if _, err := os.Stat(path); err == nil {
			return TableResult{}, fmt.Errorf("file already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return TableResult{}, fmt.Errorf("stat %s: %w", path, err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return TableResult{}, fmt.Errorf("open export %s: %w", path, err)
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	defer writer.Flush()

	dest := make([]any, len(fields))
	values := make([]any, len(fields))
	for i := range dest {
		dest[i] = &values[i]
	}

	var count int64
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return TableResult{}, fmt.Errorf("scan table %s: %w", table.Name, err)
		}
		data := make(map[string]any, len(fields))
		for i, field := range fields {
			value, err := normalizeValue(values[i], field)
			if err != nil {
				return TableResult{}, fmt.Errorf("table %s field %s: %w", table.Name, field.Name, err)
			}
			data[field.Name] = value
		}
		if err := encoder.Encode(map[string]any{"type": table.Name, "data": data}); err != nil {
			return TableResult{}, fmt.Errorf("encode table %s: %w", table.Name, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return TableResult{}, fmt.Errorf("iterate table %s: %w", table.Name, err)
	}
	if err := writer.Flush(); err != nil {
		return TableResult{}, fmt.Errorf("flush export %s: %w", path, err)
	}
	return TableResult{Table: table.Name, Path: path, Rows: count}, nil
}

func filterPlan(plan model.MigrationPlan, selected []string) model.MigrationPlan {
	if len(selected) == 0 {
		return plan
	}
	selectedSet := map[string]struct{}{}
	for _, table := range selected {
		table = strings.TrimSpace(table)
		if table != "" {
			selectedSet[table] = struct{}{}
		}
	}
	filtered := plan
	filtered.Tables = make([]model.TablePlan, 0, len(plan.Tables))
	for _, table := range plan.Tables {
		if _, ok := selectedSet[table.Name]; !ok {
			table.Include = false
		}
		filtered.Tables = append(filtered.Tables, table)
	}
	return filtered
}

func selectSQL(plan model.MigrationPlan, table model.TablePlan, fields []model.FieldPlan) string {
	sourceTable := table.SourceName
	if sourceTable == "" {
		sourceTable = table.Name
	}
	var b strings.Builder
	b.WriteString("SELECT ")
	for i, field := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		sourceName := field.SourceName
		if sourceName == "" {
			sourceName = field.Name
		}
		b.WriteString(quoteIdent(sourceName))
	}
	b.WriteString(" FROM ")
	b.WriteString(qualifiedSourceTable(plan.Source.Schema, sourceTable))
	if len(table.PrimaryKey) > 0 {
		b.WriteString(" ORDER BY ")
		for i, field := range table.PrimaryKey {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteIdent(field))
		}
	}
	return b.String()
}

func includedTablesByName(plan model.MigrationPlan) map[string]model.TablePlan {
	tables := make(map[string]model.TablePlan)
	for _, table := range plan.Tables {
		if table.Include {
			tables[table.Name] = table
		}
	}
	return tables
}

func includedFields(table model.TablePlan) []model.FieldPlan {
	var fields []model.FieldPlan
	for _, field := range table.Fields {
		if field.Include {
			fields = append(fields, field)
		}
	}
	sort.SliceStable(fields, func(i, j int) bool {
		return fields[i].SourceOrdinal < fields[j].SourceOrdinal
	})
	return fields
}

func normalizeValue(raw any, field model.FieldPlan) (any, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []byte:
		return normalizeStringValue(string(v), field)
	case string:
		return normalizeStringValue(v, field)
	case time.Time:
		if field.RecommendedMappingStrategy == "TimestampBSI" {
			return v.UTC().Format(time.RFC3339Nano), nil
		}
		return v.Format("2006-01-02 15:04:05"), nil
	default:
		return raw, nil
	}
}

func normalizeStringValue(value string, field model.FieldPlan) (any, error) {
	switch field.RecommendedMappingStrategy {
	case "IntBSI":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
		return parsed, nil
	case "FloatScaleBSI":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, err
		}
		return parsed, nil
	default:
		return value, nil
	}
}

func qualifiedSourceTable(schema, table string) string {
	if strings.Contains(table, ".") {
		parts := strings.Split(table, ".")
		quoted := make([]string, 0, len(parts))
		for _, part := range parts {
			quoted = append(quoted, quoteIdent(part))
		}
		return strings.Join(quoted, ".")
	}
	if strings.TrimSpace(schema) == "" {
		return quoteIdent(table)
	}
	return quoteIdent(schema) + "." + quoteIdent(table)
}

func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func writeText(path, body string, perm os.FileMode, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("file already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
