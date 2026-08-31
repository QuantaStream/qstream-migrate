package loadplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

func TestWriteCreatesLoadArtifactsInRelationshipOrder(t *testing.T) {
	dir := t.TempDir()
	plan := testPlan()

	result, err := Write(plan, Options{OutDir: dir, RelationshipMode: "all", LoaderTarget: "http://loader/ingest/json", BatchSize: 10})
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got, want := strings.Join(result.LoadOrder, ","), "customers,orders"; got != want {
		t.Fatalf("load order = %s, want %s", got, want)
	}
	if len(result.Files) != 6 {
		t.Fatalf("files = %d, want 6: %+v", len(result.Files), result.Files)
	}

	loadOrder := readFile(t, filepath.Join(dir, "load-order.txt"))
	if loadOrder != "customers\norders\n" {
		t.Fatalf("load-order.txt = %q", loadOrder)
	}
	ordersSQL := readFile(t, filepath.Join(dir, "queries", "orders.json.sql"))
	assertContains(t, ordersSQL, "SELECT JSON_OBJECT(")
	assertContains(t, ordersSQL, "'type', 'orders'")
	assertContains(t, ordersSQL, "'data', JSON_OBJECT(")
	assertContains(t, ordersSQL, "'order_id', `order_id`")
	assertContains(t, ordersSQL, "FROM `sales`.`orders`")
	assertContains(t, ordersSQL, "ORDER BY `order_id`;")
	readme := readFile(t, filepath.Join(dir, "README.md"))
	assertContains(t, readme, "Relationship mode: `all`")
	assertContains(t, readme, "1. `customers`")
	assertContains(t, readme, "2. `orders`")
}

func TestWriteSkipsCandidateRelationshipsByDefault(t *testing.T) {
	dir := t.TempDir()
	plan := testPlan()

	result, err := Write(plan, Options{OutDir: dir, RelationshipMode: "metadata"})
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got, want := strings.Join(result.LoadOrder, ","), "customers,orders"; got != want {
		t.Fatalf("load order = %s, want lexical order %s when candidates are skipped", got, want)
	}
}

func TestWriteErrorsWhenRelationshipParentIsNotIncluded(t *testing.T) {
	dir := t.TempDir()
	plan := testPlan()
	plan.Tables[0].Include = false

	_, err := Write(plan, Options{OutDir: dir, RelationshipMode: "all"})
	if err == nil || !strings.Contains(err.Error(), "parent table customers is not included") {
		t.Fatalf("expected missing parent error, got %v", err)
	}
}

func TestWriteErrorsOnRelationshipCycle(t *testing.T) {
	dir := t.TempDir()
	plan := testPlan()
	plan.Tables[0].Relationships = []model.RelationshipPlan{
		{Kind: "candidate_foreign_key", Name: "candidate_customers_orders", Columns: []string{"customer_id"}, ParentTable: "orders", ParentColumns: []string{"order_id"}},
	}

	_, err := Write(plan, Options{OutDir: dir, RelationshipMode: "all"})
	if err == nil || !strings.Contains(err.Error(), "relationship cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestExportSQLQuotesIdentifiersAndStrings(t *testing.T) {
	plan := model.MigrationPlan{Source: model.SourceInfo{Schema: "odd`schema"}}
	table := model.TablePlan{
		Name:       "customer's",
		SourceName: "customer's",
		Include:    true,
		PrimaryKey: []string{"id`x"},
		Fields: []model.FieldPlan{
			{Name: "id`x", SourceName: "id`x", Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", SourceOrdinal: 1},
		},
	}

	sql, err := exportSQL(plan, table)
	if err != nil {
		t.Fatalf("exportSQL returned error: %v", err)
	}
	assertContains(t, sql, "'type', 'customer''s'")
	assertContains(t, sql, "'id`x', `id``x`")
	assertContains(t, sql, "FROM `odd``schema`.`customer's`")
	assertContains(t, sql, "ORDER BY `id``x`;")
}

func testPlan() model.MigrationPlan {
	return model.MigrationPlan{
		Source: model.SourceInfo{Kind: "mysql", Schema: "sales"},
		Tables: []model.TablePlan{
			{
				Name:       "customers",
				SourceName: "customers",
				Include:    true,
				PrimaryKey: []string{"customer_id"},
				Fields: []model.FieldPlan{
					{Name: "customer_id", SourceName: "customer_id", Include: true, RecommendedMappingStrategy: "StringLexBSI", QuantaStreamType: "String", SourceOrdinal: 1},
					{Name: "name", SourceName: "name", Include: true, RecommendedMappingStrategy: "StringEnum", QuantaStreamType: "String", SourceOrdinal: 2},
				},
			},
			{
				Name:       "orders",
				SourceName: "orders",
				Include:    true,
				PrimaryKey: []string{"order_id"},
				Fields: []model.FieldPlan{
					{Name: "order_id", SourceName: "order_id", Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", SourceOrdinal: 1},
					{Name: "customer_id", SourceName: "customer_id", Include: true, RecommendedMappingStrategy: "StringLexBSI", QuantaStreamType: "String", SourceOrdinal: 2},
					{Name: "total", SourceName: "total", Include: true, RecommendedMappingStrategy: "FloatScaleBSI", QuantaStreamType: "Float", SourceOrdinal: 3},
				},
				Relationships: []model.RelationshipPlan{
					{Kind: "candidate_foreign_key", Name: "candidate_orders_customers", Columns: []string{"customer_id"}, ParentTable: "customers", ParentColumns: []string{"customer_id"}},
				},
			},
		},
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(payload)
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q in:\n%s", needle, haystack)
	}
}
