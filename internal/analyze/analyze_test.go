package analyze

import (
	"testing"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

func TestBuildPlanMappingRecommendations(t *testing.T) {
	scale := int64(2)
	distinct3 := int64(3)
	distinct900 := int64(900)
	maxSegmentLen := int64(10)
	maxCustomerIDLen := int64(12)
	commentLen := int64(900)

	inv := model.Inventory{
		Source: model.SourceInfo{Kind: "mysql", Schema: "sales"},
		Tables: []model.TableInventory{
			{
				Name:       "orders",
				PrimaryKey: []string{"row_id"},
				Indexes: []model.IndexInventory{
					{
						Name: "idx_customer_id",
						Columns: []model.IndexColumn{
							{Name: "customer_id", Ordinal: 1},
						},
					},
				},
				Columns: []model.ColumnInventory{
					{Name: "row_id", Ordinal: 1, DataType: "int", ColumnType: "int", NumericScale: &scale},
					{
						Name:       "customer_id",
						Ordinal:    2,
						DataType:   "varchar",
						ColumnType: "varchar(32)",
						Profile: &model.ColumnProfile{
							RowCount:        1000,
							DistinctCount:   &distinct900,
							MaxStringLength: &maxCustomerIDLen,
						},
					},
					{
						Name:       "segment",
						Ordinal:    3,
						DataType:   "varchar",
						ColumnType: "varchar(32)",
						Profile: &model.ColumnProfile{
							RowCount:        1000,
							DistinctCount:   &distinct3,
							MaxStringLength: &maxSegmentLen,
						},
					},
					{Name: "notes", Ordinal: 4, DataType: "text", ColumnType: "text", Profile: &model.ColumnProfile{MaxStringLength: &commentLen}},
					{Name: "amount", Ordinal: 5, DataType: "decimal", ColumnType: "decimal(12,2)", NumericScale: &scale},
					{Name: "created_at", Ordinal: 6, DataType: "datetime", ColumnType: "datetime"},
				},
			},
		},
	}

	plan := BuildPlan(inv, Options{StringEnumMaxDistinct: 500, DefaultLexPrefixLength: 16})
	if len(plan.Tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(plan.Tables))
	}
	fields := fieldsByName(plan.Tables[0])

	assertMapping(t, fields["row_id"], "IntBSI", "Integer")
	if !fields["row_id"].ColumnID {
		t.Fatalf("row_id should be marked as column_id")
	}

	assertMapping(t, fields["customer_id"], "StringLexBSI", "String")
	if fields["customer_id"].LexPrefixLength == nil || *fields["customer_id"].LexPrefixLength != 12 {
		t.Fatalf("customer_id lex prefix = %v, want 12", fields["customer_id"].LexPrefixLength)
	}

	assertMapping(t, fields["segment"], "StringEnum", "String")
	assertMapping(t, fields["notes"], "ReviewText", "String")
	assertMapping(t, fields["amount"], "FloatScaleBSI", "Float")
	if fields["amount"].Scale == nil || *fields["amount"].Scale != 2 {
		t.Fatalf("amount scale = %v, want 2", fields["amount"].Scale)
	}
	assertMapping(t, fields["created_at"], "TimestampBSI", "Date")
}

func TestBuildPlanRelationshipsFromForeignKeys(t *testing.T) {
	inv := model.Inventory{
		Source: model.SourceInfo{Kind: "mysql", Schema: "sales"},
		Tables: []model.TableInventory{
			{
				Name:       "orders",
				PrimaryKey: []string{"order_id"},
				ForeignKeys: []model.ForeignKey{
					{
						Name:          "orders_customer_fk",
						Columns:       []string{"customer_id"},
						ParentTable:   "customers",
						ParentColumns: []string{"customer_id"},
					},
				},
				Columns: []model.ColumnInventory{
					{Name: "order_id", Ordinal: 1, DataType: "int", ColumnType: "int"},
					{Name: "customer_id", Ordinal: 2, DataType: "varchar", ColumnType: "varchar(16)"},
				},
			},
		},
	}

	plan := BuildPlan(inv, Options{})
	if len(plan.Tables[0].Relationships) != 1 {
		t.Fatalf("relationships = %d, want 1", len(plan.Tables[0].Relationships))
	}
	rel := plan.Tables[0].Relationships[0]
	if rel.Kind != "foreign_key" || rel.Confidence != "metadata" || rel.ParentTable != "customers" {
		t.Fatalf("unexpected relationship: %+v", rel)
	}

	fields := fieldsByName(plan.Tables[0])
	assertMapping(t, fields["customer_id"], "StringLexBSI", "String")
}

func TestBuildPlanCandidateRelationshipsFromNames(t *testing.T) {
	inv := model.Inventory{
		Source: model.SourceInfo{Kind: "mysql", Schema: "sales"},
		Tables: []model.TableInventory{
			{
				Name:       "customers",
				PrimaryKey: []string{"id"},
				Columns: []model.ColumnInventory{
					{Name: "id", Ordinal: 1, DataType: "int", ColumnType: "int"},
				},
			},
			{
				Name:       "orders",
				PrimaryKey: []string{"id"},
				Columns: []model.ColumnInventory{
					{Name: "id", Ordinal: 1, DataType: "int", ColumnType: "int"},
					{Name: "customer_id", Ordinal: 2, DataType: "int", ColumnType: "int"},
				},
			},
		},
	}

	plan := BuildPlan(inv, Options{})
	var orders model.TablePlan
	for _, table := range plan.Tables {
		if table.Name == "orders" {
			orders = table
		}
	}
	if len(orders.Relationships) != 1 {
		t.Fatalf("candidate relationships = %d, want 1: %+v", len(orders.Relationships), orders.Relationships)
	}
	rel := orders.Relationships[0]
	if rel.Kind != "candidate_foreign_key" || rel.Confidence != "name_and_type_match" || rel.ParentTable != "customers" {
		t.Fatalf("unexpected candidate relationship: %+v", rel)
	}
}

func TestBuildPlanCandidateRelationshipsFromPrefixedKeys(t *testing.T) {
	inv := model.Inventory{
		Source: model.SourceInfo{Kind: "mysql", Schema: "tpch"},
		Tables: []model.TableInventory{
			{
				Name:       "customer",
				PrimaryKey: []string{"c_custkey"},
				Columns: []model.ColumnInventory{
					{Name: "c_custkey", Ordinal: 1, DataType: "int", ColumnType: "int"},
				},
			},
			{
				Name:       "orders",
				PrimaryKey: []string{"o_orderkey"},
				Columns: []model.ColumnInventory{
					{Name: "o_orderkey", Ordinal: 1, DataType: "int", ColumnType: "int"},
					{Name: "o_custkey", Ordinal: 2, DataType: "int", ColumnType: "int"},
				},
			},
		},
	}

	plan := BuildPlan(inv, Options{})
	var orders model.TablePlan
	for _, table := range plan.Tables {
		if table.Name == "orders" {
			orders = table
		}
	}
	if len(orders.Relationships) != 1 {
		t.Fatalf("candidate relationships = %d, want 1: %+v", len(orders.Relationships), orders.Relationships)
	}
	rel := orders.Relationships[0]
	if rel.Columns[0] != "o_custkey" || rel.ParentTable != "customer" || rel.ParentColumns[0] != "c_custkey" {
		t.Fatalf("unexpected prefixed-key relationship: %+v", rel)
	}
}

func TestBuildPlanCandidateRelationshipsFromCompositePrimaryKeyColumns(t *testing.T) {
	inv := model.Inventory{
		Source: model.SourceInfo{Kind: "mysql", Schema: "tpch"},
		Tables: []model.TableInventory{
			{
				Name:       "orders",
				PrimaryKey: []string{"o_orderkey"},
				Columns: []model.ColumnInventory{
					{Name: "o_orderkey", Ordinal: 1, DataType: "int", ColumnType: "int"},
				},
			},
			{
				Name:       "lineitem",
				PrimaryKey: []string{"l_orderkey", "l_linenumber"},
				Columns: []model.ColumnInventory{
					{Name: "l_orderkey", Ordinal: 1, DataType: "int", ColumnType: "int"},
					{Name: "l_linenumber", Ordinal: 2, DataType: "int", ColumnType: "int"},
				},
			},
		},
	}

	plan := BuildPlan(inv, Options{})
	var lineitem model.TablePlan
	for _, table := range plan.Tables {
		if table.Name == "lineitem" {
			lineitem = table
		}
	}
	if len(lineitem.Relationships) != 1 {
		t.Fatalf("candidate relationships = %d, want 1: %+v", len(lineitem.Relationships), lineitem.Relationships)
	}
	rel := lineitem.Relationships[0]
	if rel.Columns[0] != "l_orderkey" || rel.ParentTable != "orders" || rel.ParentColumns[0] != "o_orderkey" {
		t.Fatalf("unexpected composite-key relationship: %+v", rel)
	}
}

func fieldsByName(table model.TablePlan) map[string]model.FieldPlan {
	fields := make(map[string]model.FieldPlan, len(table.Fields))
	for _, field := range table.Fields {
		fields[field.Name] = field
	}
	return fields
}

func assertMapping(t *testing.T, field model.FieldPlan, strategy, qsType string) {
	t.Helper()
	if field.RecommendedMappingStrategy != strategy || field.QuantaStreamType != qsType {
		t.Fatalf("%s mapping = %s/%s, want %s/%s", field.Name, field.RecommendedMappingStrategy, field.QuantaStreamType, strategy, qsType)
	}
}
