package validate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

type CountOptions struct {
	Tables []string
}

type CountResult struct {
	Table      string
	SourceRows int64
	TargetRows int64
	Match      bool
}

type CountsResult struct {
	Tables     []CountResult
	Mismatches int
}

type CountFunc func(context.Context, model.TablePlan) (int64, error)

func CompareCounts(ctx context.Context, plan model.MigrationPlan, opts CountOptions, sourceCount, targetCount CountFunc) (CountsResult, error) {
	selected := selectedTables(opts.Tables)
	var result CountsResult
	for _, table := range plan.Tables {
		if !table.Include || !selected.include(table.Name) {
			continue
		}
		sourceRows, err := sourceCount(ctx, table)
		if err != nil {
			return CountsResult{}, fmt.Errorf("source count table %s: %w", table.Name, err)
		}
		targetRows, err := targetCount(ctx, table)
		if err != nil {
			return CountsResult{}, fmt.Errorf("target count table %s: %w", table.Name, err)
		}
		tableResult := CountResult{
			Table:      table.Name,
			SourceRows: sourceRows,
			TargetRows: targetRows,
			Match:      sourceRows == targetRows,
		}
		if !tableResult.Match {
			result.Mismatches++
		}
		result.Tables = append(result.Tables, tableResult)
	}
	sort.SliceStable(result.Tables, func(i, j int) bool {
		return result.Tables[i].Table < result.Tables[j].Table
	})
	if len(result.Tables) == 0 {
		return CountsResult{}, fmt.Errorf("no included tables selected for validation")
	}
	return result, nil
}

func (r CountsResult) Pass() bool {
	return r.Mismatches == 0
}

func (r CountsResult) Status() string {
	if r.Pass() {
		return "PASS"
	}
	return "FAIL"
}

func FormatCounts(result CountsResult) []string {
	lines := make([]string, 0, len(result.Tables))
	for _, table := range result.Tables {
		status := "MATCH"
		if !table.Match {
			status = "MISMATCH"
		}
		lines = append(lines, fmt.Sprintf("validate_counts table=%s source_rows=%d target_rows=%d result=%s",
			table.Table, table.SourceRows, table.TargetRows, status))
	}
	return lines
}

func SourceCountSQL(plan model.MigrationPlan, table model.TablePlan) string {
	sourceTable := table.SourceName
	if sourceTable == "" {
		sourceTable = table.Name
	}
	return "SELECT COUNT(*) FROM " + qualifiedSourceTable(plan.Source.Schema, sourceTable)
}

func TargetCountSQL(table model.TablePlan) string {
	return "SELECT COUNT(*) FROM " + quoteIdent(table.Name)
}

type selection map[string]struct{}

func selectedTables(tables []string) selection {
	if len(tables) == 0 {
		return nil
	}
	selected := make(selection)
	for _, table := range tables {
		table = strings.TrimSpace(table)
		if table != "" {
			selected[table] = struct{}{}
		}
	}
	return selected
}

func (s selection) include(table string) bool {
	if len(s) == 0 {
		return true
	}
	_, ok := s[table]
	return ok
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
