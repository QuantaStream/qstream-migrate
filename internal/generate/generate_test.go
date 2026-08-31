package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

func TestWriteSchemasUsesMetadataRelationships(t *testing.T) {
	dir := t.TempDir()
	plan := model.MigrationPlan{
		Settings: model.AnalyzerSettings{DefaultLexPrefixLength: 16},
		Tables: []model.TablePlan{
			{
				Name:       "orders",
				SourceName: "orders",
				Include:    true,
				PrimaryKey: []string{"o_orderkey"},
				Fields: []model.FieldPlan{
					{Name: "o_orderkey", SourceName: "o_orderkey", Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", ColumnID: true, SourceOrdinal: 1},
					{Name: "o_custkey", SourceName: "o_custkey", Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", SourceOrdinal: 2},
					{Name: "o_orderdate", SourceName: "o_orderdate", Include: true, RecommendedMappingStrategy: "TimestampBSI", QuantaStreamType: "DateTime", SourceOrdinal: 3},
				},
				Relationships: []model.RelationshipPlan{
					{Kind: "foreign_key", Name: "orders_customer_fk", Columns: []string{"o_custkey"}, ParentTable: "customer", ParentColumns: []string{"c_custkey"}},
				},
			},
		},
	}

	result, err := WriteSchemas(plan, Options{OutDir: dir, RelationshipMode: "metadata", Overwrite: true})
	if err != nil {
		t.Fatalf("WriteSchemas returned error: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(result.Files))
	}

	payload := readFile(t, filepath.Join(dir, "orders", "schema.yaml"))
	assertContains(t, payload, "tableName: orders\n")
	assertContains(t, payload, "primaryKey: o_orderkey\n")
	assertContains(t, payload, "selector: type=\"orders\"\n")
	assertContains(t, payload, "fieldName: o_orderkey\n")
	assertContains(t, payload, "columnID: true\n")
	assertContains(t, payload, "fieldName: o_custkey\n")
	assertContains(t, payload, "mappingStrategy: ParentRelation\n")
	assertContains(t, payload, "foreignKey: customer.c_custkey\n")
	assertContains(t, payload, "parentToChild: true\n")
	assertContains(t, payload, "fieldName: o_orderdate\n")
	assertContains(t, payload, "granularity: millisecond\n")
}

func TestWriteSchemasEmitsParentColumnForStringNaturalKeyRelationships(t *testing.T) {
	dir := t.TempDir()
	maxLen := int64(7)
	plan := model.MigrationPlan{
		Settings: model.AnalyzerSettings{DefaultLexPrefixLength: 16},
		Tables: []model.TablePlan{
			{
				Name:       "orders",
				SourceName: "orders",
				Include:    true,
				PrimaryKey: []string{"order_id"},
				Fields: []model.FieldPlan{
					{Name: "order_id", SourceName: "order_id", Include: true, RecommendedMappingStrategy: "StringLexBSI", QuantaStreamType: "String", MaxLen: &maxLen, LexPrefixLength: intPtr(8), SourceOrdinal: 1},
					{Name: "region", SourceName: "region", Include: true, RecommendedMappingStrategy: "StringLexBSI", QuantaStreamType: "String", MaxLen: &maxLen, LexPrefixLength: intPtr(8), SourceOrdinal: 2},
				},
				Relationships: []model.RelationshipPlan{
					{Kind: "foreign_key", Name: "orders_people_fk", Columns: []string{"region"}, ParentTable: "people", ParentColumns: []string{"region"}},
				},
			},
		},
	}

	if _, err := WriteSchemas(plan, Options{OutDir: dir, RelationshipMode: "metadata", Overwrite: true}); err != nil {
		t.Fatalf("WriteSchemas returned error: %v", err)
	}
	payload := readFile(t, filepath.Join(dir, "orders", "schema.yaml"))
	assertContains(t, payload, "fieldName: region\n")
	assertContains(t, payload, "mappingStrategy: ParentRelation\n")
	assertContains(t, payload, "foreignKey: people.region\n")
}

func TestWriteSchemasEmitsReviewedTimeQuantum(t *testing.T) {
	dir := t.TempDir()
	plan := model.MigrationPlan{
		Settings: model.AnalyzerSettings{DefaultLexPrefixLength: 16},
		Tables: []model.TablePlan{
			{
				Name:        "orders",
				SourceName:  "orders",
				Include:     true,
				PrimaryKey:  []string{"o_orderkey"},
				TimeQuantum: &model.TimeQuantumPlan{Field: "o_orderdate", Type: "YMD", CandidateFields: []string{"o_orderdate"}},
				Fields: []model.FieldPlan{
					{Name: "o_orderkey", SourceName: "o_orderkey", Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", ColumnID: true, SourceOrdinal: 1},
					{Name: "o_orderdate", SourceName: "o_orderdate", Include: true, RecommendedMappingStrategy: "TimestampBSI", QuantaStreamType: "DateTime", SourceOrdinal: 2},
				},
			},
		},
	}

	if _, err := WriteSchemas(plan, Options{OutDir: dir, Overwrite: true}); err != nil {
		t.Fatalf("WriteSchemas returned error: %v", err)
	}
	payload := readFile(t, filepath.Join(dir, "orders", "schema.yaml"))
	assertContains(t, payload, "timeQuantumType: YMD\n")
	assertContains(t, payload, "timeQuantumField: o_orderdate\n")
}

func TestWriteSchemasSkipsUnselectedTimeQuantumReview(t *testing.T) {
	dir := t.TempDir()
	plan := model.MigrationPlan{
		Settings: model.AnalyzerSettings{DefaultLexPrefixLength: 16},
		Tables: []model.TablePlan{
			{
				Name:        "orders",
				SourceName:  "orders",
				Include:     true,
				PrimaryKey:  []string{"o_orderkey"},
				TimeQuantum: &model.TimeQuantumPlan{Type: "YMD", CandidateFields: []string{"o_orderdate"}},
				Fields: []model.FieldPlan{
					{Name: "o_orderkey", SourceName: "o_orderkey", Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", ColumnID: true, SourceOrdinal: 1},
					{Name: "o_orderdate", SourceName: "o_orderdate", Include: true, RecommendedMappingStrategy: "TimestampBSI", QuantaStreamType: "DateTime", SourceOrdinal: 2},
				},
			},
		},
	}

	if _, err := WriteSchemas(plan, Options{OutDir: dir, Overwrite: true}); err != nil {
		t.Fatalf("WriteSchemas returned error: %v", err)
	}
	payload := readFile(t, filepath.Join(dir, "orders", "schema.yaml"))
	if strings.Contains(payload, "timeQuantum") {
		t.Fatalf("unselected time quantum should not be emitted:\n%s", payload)
	}
}

func TestWriteSchemasSkipsCandidateRelationshipsByDefault(t *testing.T) {
	dir := t.TempDir()
	plan := oneStringTablePlan("orders", "o_orderkey", "o_clerk")
	plan.Tables[0].Relationships = []model.RelationshipPlan{
		{Kind: "candidate_foreign_key", Name: "candidate", Columns: []string{"o_clerk"}, ParentTable: "clerks", ParentColumns: []string{"clerk_id"}},
	}

	if _, err := WriteSchemas(plan, Options{OutDir: dir, RelationshipMode: "metadata", Overwrite: true}); err != nil {
		t.Fatalf("WriteSchemas returned error: %v", err)
	}
	payload := readFile(t, filepath.Join(dir, "orders", "schema.yaml"))
	assertContains(t, payload, "mappingStrategy: StringLexBSI\n")
	if strings.Contains(payload, "ParentRelation") {
		t.Fatalf("candidate relationship was generated by default:\n%s", payload)
	}
}

func TestWriteSchemasCanIncludeCandidateRelationships(t *testing.T) {
	dir := t.TempDir()
	plan := oneStringTablePlan("orders", "o_orderkey", "o_clerk")
	plan.Tables[0].Relationships = []model.RelationshipPlan{
		{Kind: "candidate_foreign_key", Name: "candidate", Columns: []string{"o_clerk"}, ParentTable: "clerks", ParentColumns: []string{"clerk_id"}},
	}

	if _, err := WriteSchemas(plan, Options{OutDir: dir, RelationshipMode: "all", Overwrite: true}); err != nil {
		t.Fatalf("WriteSchemas returned error: %v", err)
	}
	payload := readFile(t, filepath.Join(dir, "orders", "schema.yaml"))
	assertContains(t, payload, "fieldName: o_clerk\n")
	assertContains(t, payload, "mappingStrategy: ParentRelation\n")
	assertContains(t, payload, "foreignKey: clerks.clerk_id\n")
}

func TestWriteSchemasRejectsReviewMappings(t *testing.T) {
	dir := t.TempDir()
	plan := model.MigrationPlan{
		Tables: []model.TablePlan{
			{
				Name:       "notes",
				SourceName: "notes",
				Include:    true,
				PrimaryKey: []string{"id"},
				Fields: []model.FieldPlan{
					{Name: "id", SourceName: "id", Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", SourceOrdinal: 1},
					{Name: "body", SourceName: "body", Include: true, RecommendedMappingStrategy: "ReviewText", QuantaStreamType: "String", SourceOrdinal: 2},
				},
			},
		},
	}

	_, err := WriteSchemas(plan, Options{OutDir: dir, Overwrite: true})
	if err == nil || !strings.Contains(err.Error(), "requires plan review") {
		t.Fatalf("expected review mapping error, got %v", err)
	}
}

func intPtr(value int) *int {
	return &value
}

func oneStringTablePlan(tableName, pk, stringField string) model.MigrationPlan {
	lexLength := 12
	maxLen := int64(32)
	return model.MigrationPlan{
		Settings: model.AnalyzerSettings{DefaultLexPrefixLength: 16},
		Tables: []model.TablePlan{
			{
				Name:       tableName,
				SourceName: tableName,
				Include:    true,
				PrimaryKey: []string{pk},
				Fields: []model.FieldPlan{
					{Name: pk, SourceName: pk, Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", ColumnID: true, SourceOrdinal: 1},
					{Name: stringField, SourceName: stringField, Include: true, RecommendedMappingStrategy: "StringLexBSI", QuantaStreamType: "String", MaxLen: &maxLen, LexPrefixLength: &lexLength, SourceOrdinal: 2},
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
