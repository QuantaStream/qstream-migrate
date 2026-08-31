package output

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

func TestWriteAnalysis(t *testing.T) {
	dir := t.TempDir()
	inv := model.Inventory{
		Version:     1,
		GeneratedAt: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
		Source:      model.SourceInfo{Kind: "mysql", Schema: "sales", DSNMasked: "u:xxxxx@tcp(localhost:3306)/sales"},
		Tables: []model.TableInventory{
			{Name: "orders", PrimaryKey: []string{"id"}, Columns: []model.ColumnInventory{{Name: "id"}}},
		},
	}
	plan := model.MigrationPlan{
		Version:     1,
		GeneratedAt: inv.GeneratedAt,
		Source:      inv.Source,
		Tables: []model.TablePlan{
			{Name: "orders", SourceName: "orders", Include: true, Fields: []model.FieldPlan{{Name: "id"}}},
		},
	}

	paths, err := WriteAnalysis(dir, inv, plan)
	if err != nil {
		t.Fatalf("WriteAnalysis returned error: %v", err)
	}
	for _, path := range []string{paths.Inventory, paths.Plan, paths.Readme} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected file %s: %v", path, err)
		}
		if filepath.Dir(path) != dir {
			t.Fatalf("path %s not under temp dir %s", path, dir)
		}
	}
}
