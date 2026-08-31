package validate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

func TestCompareCountsPassesWhenCountsMatch(t *testing.T) {
	plan := countPlan()
	counts := map[string]int64{"orders": 10, "customers": 3}

	result, err := CompareCounts(context.Background(), plan, CountOptions{}, mapCounter(counts), mapCounter(counts))
	if err != nil {
		t.Fatalf("CompareCounts returned error: %v", err)
	}
	if !result.Pass() || result.Mismatches != 0 {
		t.Fatalf("result = %+v, want pass", result)
	}
	lines := FormatCounts(result)
	if len(lines) != 2 || !strings.Contains(lines[0], "result=MATCH") {
		t.Fatalf("unexpected lines: %+v", lines)
	}
}

func TestCompareCountsReportsMismatches(t *testing.T) {
	plan := countPlan()

	result, err := CompareCounts(
		context.Background(),
		plan,
		CountOptions{},
		mapCounter(map[string]int64{"orders": 10, "customers": 3}),
		mapCounter(map[string]int64{"orders": 9, "customers": 3}),
	)
	if err != nil {
		t.Fatalf("CompareCounts returned error: %v", err)
	}
	if result.Pass() || result.Mismatches != 1 || result.Status() != "FAIL" {
		t.Fatalf("result = %+v, want one mismatch", result)
	}
	lines := strings.Join(FormatCounts(result), "\n")
	if !strings.Contains(lines, "table=orders") || !strings.Contains(lines, "result=MISMATCH") {
		t.Fatalf("missing mismatch line:\n%s", lines)
	}
}

func TestCompareCountsFiltersTables(t *testing.T) {
	plan := countPlan()
	result, err := CompareCounts(
		context.Background(),
		plan,
		CountOptions{Tables: []string{"customers"}},
		mapCounter(map[string]int64{"orders": 10, "customers": 3}),
		mapCounter(map[string]int64{"orders": 9, "customers": 3}),
	)
	if err != nil {
		t.Fatalf("CompareCounts returned error: %v", err)
	}
	if len(result.Tables) != 1 || result.Tables[0].Table != "customers" || !result.Pass() {
		t.Fatalf("result = %+v, want customers only pass", result)
	}
}

func TestCompareCountsReturnsSourceErrors(t *testing.T) {
	plan := countPlan()
	want := errors.New("boom")
	_, err := CompareCounts(
		context.Background(),
		plan,
		CountOptions{},
		func(context.Context, model.TablePlan) (int64, error) { return 0, want },
		mapCounter(map[string]int64{}),
	)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestCountSQL(t *testing.T) {
	plan := model.MigrationPlan{Source: model.SourceInfo{Schema: "odd`schema"}}
	table := model.TablePlan{Name: "orders", SourceName: "odd`table"}

	if got, want := SourceCountSQL(plan, table), "SELECT COUNT(*) FROM `odd``schema`.`odd``table`"; got != want {
		t.Fatalf("source sql = %q, want %q", got, want)
	}
	if got, want := TargetCountSQL(table), "SELECT COUNT(*) FROM `orders`"; got != want {
		t.Fatalf("target sql = %q, want %q", got, want)
	}
}

func countPlan() model.MigrationPlan {
	return model.MigrationPlan{
		Tables: []model.TablePlan{
			{Name: "orders", SourceName: "orders", Include: true},
			{Name: "customers", SourceName: "customers", Include: true},
			{Name: "ignored", SourceName: "ignored", Include: false},
		},
	}
}

func mapCounter(counts map[string]int64) CountFunc {
	return func(_ context.Context, table model.TablePlan) (int64, error) {
		return counts[table.Name], nil
	}
}
