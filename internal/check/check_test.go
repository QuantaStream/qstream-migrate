package check

import (
	"strings"
	"testing"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

func TestCheckPlanPassesReadyPlan(t *testing.T) {
	plan := readyPlan()

	result := CheckPlan(plan, Options{RelationshipMode: "metadata"})
	if !result.Pass(false) {
		t.Fatalf("plan should pass: %+v", result)
	}
	if result.Errors != 0 || result.Warnings != 0 {
		t.Fatalf("errors/warnings = %d/%d, want 0/0", result.Errors, result.Warnings)
	}
}

func TestCheckPlanReportsUnresolvedMappingsAndMissingPrimaryKey(t *testing.T) {
	plan := model.MigrationPlan{
		Tables: []model.TablePlan{
			{
				Name:       "notes",
				SourceName: "notes",
				Include:    true,
				Fields: []model.FieldPlan{
					{Name: "id", SourceName: "id", Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", SourceOrdinal: 1},
					{Name: "body", SourceName: "body", Include: true, RecommendedMappingStrategy: "ReviewText", QuantaStreamType: "String", SourceOrdinal: 2},
				},
			},
		},
	}

	result := CheckPlan(plan, Options{})
	if result.Pass(false) {
		t.Fatalf("plan should fail: %+v", result)
	}
	assertIssue(t, result, "missing_primary_key")
	assertIssue(t, result, "unresolved_mapping")
}

func TestCheckPlanWarnsForUnselectedTimeQuantum(t *testing.T) {
	plan := readyPlan()
	plan.Tables[0].TimeQuantum = &model.TimeQuantumPlan{Type: "YMD", CandidateFields: []string{"created_at"}}

	result := CheckPlan(plan, Options{})
	if !result.Pass(false) {
		t.Fatalf("warnings should not fail non-strict check: %+v", result)
	}
	if result.Pass(true) {
		t.Fatalf("warnings should fail strict check: %+v", result)
	}
	assertIssue(t, result, "time_quantum_unselected")
}

func TestCheckPlanWarnsForCandidateRelationshipsByMode(t *testing.T) {
	plan := readyPlan()
	plan.Tables = append(plan.Tables, model.TablePlan{
		Name:       "customers",
		SourceName: "customers",
		Include:    true,
		PrimaryKey: []string{"customer_id"},
		Fields: []model.FieldPlan{
			{Name: "customer_id", SourceName: "customer_id", Include: true, RecommendedMappingStrategy: "StringLexBSI", QuantaStreamType: "String", LexPrefixLength: intPtr(12), SourceOrdinal: 1},
		},
	})
	plan.Tables[0].Fields = append(plan.Tables[0].Fields,
		model.FieldPlan{Name: "customer_id", SourceName: "customer_id", Include: true, RecommendedMappingStrategy: "StringLexBSI", QuantaStreamType: "String", LexPrefixLength: intPtr(12), SourceOrdinal: 3},
	)
	plan.Tables[0].Relationships = []model.RelationshipPlan{
		{Kind: "candidate_foreign_key", Name: "candidate_orders_customer", Columns: []string{"customer_id"}, ParentTable: "customers", ParentColumns: []string{"customer_id"}},
	}

	metadataResult := CheckPlan(plan, Options{RelationshipMode: "metadata"})
	assertIssue(t, metadataResult, "candidate_relationships_skipped")

	allResult := CheckPlan(plan, Options{RelationshipMode: "all"})
	assertIssue(t, allResult, "candidate_relationship_enabled")
}

func TestCheckPlanWarnsForExcludedRelationship(t *testing.T) {
	plan := readyPlan()
	plan.Tables[0].Relationships = []model.RelationshipPlan{{
		Name: "fk_orders_customers", Kind: "foreign_key", Exclude: true,
	}}

	result := CheckPlan(plan, Options{RelationshipMode: "metadata"})
	assertIssue(t, result, "relationship_excluded")
}

func TestFormatIssuesIncludesContext(t *testing.T) {
	result := Result{}
	result.add(Issue{
		Severity: SeverityWarn,
		Code:     "time_quantum_unselected",
		Table:    "orders",
		Detail:   "review this",
	})

	lines := FormatIssues(result)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "plan_check WARN") ||
		!strings.Contains(lines[0], "code=time_quantum_unselected") ||
		!strings.Contains(lines[0], "table=orders") {
		t.Fatalf("unexpected line: %s", lines[0])
	}
}

func readyPlan() model.MigrationPlan {
	return model.MigrationPlan{
		Tables: []model.TablePlan{
			{
				Name:       "orders",
				SourceName: "orders",
				Include:    true,
				PrimaryKey: []string{"order_id"},
				Fields: []model.FieldPlan{
					{Name: "order_id", SourceName: "order_id", Include: true, RecommendedMappingStrategy: "IntBSI", QuantaStreamType: "Integer", ColumnID: true, SourceOrdinal: 1},
					{Name: "created_at", SourceName: "created_at", Include: true, RecommendedMappingStrategy: "TimestampBSI", QuantaStreamType: "Date", SourceOrdinal: 2},
				},
			},
		},
	}
}

func assertIssue(t *testing.T, result Result, code string) {
	t.Helper()
	for _, issue := range result.Issues {
		if issue.Code == code {
			return
		}
	}
	t.Fatalf("issue %q not found in %+v", code, result.Issues)
}

func intPtr(value int) *int {
	return &value
}
