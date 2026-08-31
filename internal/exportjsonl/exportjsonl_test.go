package exportjsonl

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

func TestSelectSQLUsesIncludedFieldsInSourceOrder(t *testing.T) {
	plan := model.MigrationPlan{Source: model.SourceInfo{Schema: "sales"}}
	table := model.TablePlan{
		Name:       "orders",
		SourceName: "orders",
		PrimaryKey: []string{"order_id"},
		Fields: []model.FieldPlan{
			{Name: "total", SourceName: "total", Include: true, SourceOrdinal: 3},
			{Name: "ignored", SourceName: "ignored", Include: false, SourceOrdinal: 2},
			{Name: "order_id", SourceName: "order_id", Include: true, SourceOrdinal: 1},
		},
	}

	sql := selectSQL(plan, table, includedFields(table))
	want := "SELECT `order_id`, `total` FROM `sales`.`orders` ORDER BY `order_id`"
	if sql != want {
		t.Fatalf("sql = %q, want %q", sql, want)
	}
}

func TestSelectSQLQuotesIdentifiers(t *testing.T) {
	plan := model.MigrationPlan{Source: model.SourceInfo{Schema: "odd`schema"}}
	table := model.TablePlan{
		Name:       "events",
		SourceName: "odd`table",
		PrimaryKey: []string{"id`x"},
		Fields: []model.FieldPlan{
			{Name: "id`x", SourceName: "id`x", Include: true, SourceOrdinal: 1},
		},
	}

	sql := selectSQL(plan, table, includedFields(table))
	want := "SELECT `id``x` FROM `odd``schema`.`odd``table` ORDER BY `id``x`"
	if sql != want {
		t.Fatalf("sql = %q, want %q", sql, want)
	}
}

func TestNormalizeValueUsesMappingStrategy(t *testing.T) {
	tests := []struct {
		name  string
		raw   any
		field model.FieldPlan
		want  any
	}{
		{
			name:  "int bytes",
			raw:   []byte("42"),
			field: model.FieldPlan{RecommendedMappingStrategy: "IntBSI"},
			want:  int64(42),
		},
		{
			name:  "float string",
			raw:   "123.45",
			field: model.FieldPlan{RecommendedMappingStrategy: "FloatScaleBSI"},
			want:  float64(123.45),
		},
		{
			name:  "string bytes",
			raw:   []byte("BUILDING"),
			field: model.FieldPlan{RecommendedMappingStrategy: "StringEnum"},
			want:  "BUILDING",
		},
		{
			name:  "timestamp time",
			raw:   time.Date(2026, 8, 31, 12, 34, 56, 0, time.FixedZone("test", -6*60*60)),
			field: model.FieldPlan{RecommendedMappingStrategy: "TimestampBSI"},
			want:  "2026-08-31T18:34:56Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeValue(tt.raw, tt.field)
			if err != nil {
				t.Fatalf("normalizeValue returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeValue = %#v (%T), want %#v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func TestNormalizeValueReportsInvalidNumbers(t *testing.T) {
	_, err := normalizeValue("not-a-number", model.FieldPlan{RecommendedMappingStrategy: "IntBSI"})
	if err == nil || !strings.Contains(err.Error(), "invalid syntax") {
		t.Fatalf("expected invalid number error, got %v", err)
	}
}

func TestFilterPlanMarksUnselectedTablesExcluded(t *testing.T) {
	plan := model.MigrationPlan{
		Tables: []model.TablePlan{
			{Name: "orders", Include: true},
			{Name: "customers", Include: true},
		},
	}

	filtered := filterPlan(plan, []string{"orders"})
	if !filtered.Tables[0].Include {
		t.Fatalf("selected table should remain included")
	}
	if filtered.Tables[1].Include {
		t.Fatalf("unselected table should be excluded")
	}
}
