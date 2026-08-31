package schemacmp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareDirsMatchesEquivalentSchemas(t *testing.T) {
	ref := t.TempDir()
	gen := t.TempDir()
	writeSchema(t, ref, "orders", ordersSchema("StringEnum", "DateTime", "YMD", "o_orderdate"))
	writeSchema(t, gen, "orders", ordersSchema("StringEnum", "DateTime", "YMD", "o_orderdate"))

	result, err := CompareDirs(ref, gen)
	if err != nil {
		t.Fatalf("CompareDirs returned error: %v", err)
	}
	if !result.Match() {
		t.Fatalf("schemas should match: %+v", result.Differences)
	}
	if result.TablesCompared != 1 {
		t.Fatalf("tables compared = %d, want 1", result.TablesCompared)
	}
}

func TestCompareDirsReportsPropertyDifferences(t *testing.T) {
	ref := t.TempDir()
	gen := t.TempDir()
	writeSchema(t, ref, "orders", ordersSchema("StringEnum", "DateTime", "YMD", "o_orderdate"))
	writeSchema(t, gen, "orders", ordersSchema("StringLexBSI", "Date", "", ""))

	result, err := CompareDirs(ref, gen)
	if err != nil {
		t.Fatalf("CompareDirs returned error: %v", err)
	}
	if result.Match() {
		t.Fatalf("schemas should differ")
	}
	assertDifference(t, result, "property_diff", "orders", "o_clerk", "mappingStrategy")
	assertDifference(t, result, "property_diff", "orders", "o_orderdate", "type")
	assertDifference(t, result, "property_diff", "orders", "", "timeQuantumField")
}

func TestCompareDirsReportsMissingAndExtraTables(t *testing.T) {
	ref := t.TempDir()
	gen := t.TempDir()
	writeSchema(t, ref, "orders", ordersSchema("StringEnum", "DateTime", "", ""))
	writeSchema(t, gen, "lineitem", strings.Replace(ordersSchema("StringEnum", "DateTime", "", ""), "tableName: orders", "tableName: lineitem", 1))

	result, err := CompareDirs(ref, gen)
	if err != nil {
		t.Fatalf("CompareDirs returned error: %v", err)
	}
	assertDifference(t, result, "missing_table", "orders", "", "")
	assertDifference(t, result, "extra_table", "lineitem", "", "")
}

func TestFormatDifferencesIncludesContext(t *testing.T) {
	result := Result{
		Differences: []Difference{
			{Code: "property_diff", Table: "orders", Field: "o_orderdate", Property: "type", Reference: "DateTime", Generated: "Date"},
		},
	}

	lines := FormatDifferences(result)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "schema_compare DIFF") ||
		!strings.Contains(lines[0], "table=orders") ||
		!strings.Contains(lines[0], "field=o_orderdate") ||
		!strings.Contains(lines[0], "property=type") {
		t.Fatalf("unexpected line: %s", lines[0])
	}
}

func writeSchema(t *testing.T, root, table, body string) {
	t.Helper()
	dir := filepath.Join(root, table)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "schema.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func ordersSchema(clerkMapping, dateType, timeQuantumType, timeQuantumField string) string {
	var b strings.Builder
	b.WriteString("tableName: orders\n")
	b.WriteString("primaryKey: o_orderkey\n")
	b.WriteString("selector: type=\"orders\"\n")
	if timeQuantumType != "" {
		b.WriteString("timeQuantumType: " + timeQuantumType + "\n")
	}
	if timeQuantumField != "" {
		b.WriteString("timeQuantumField: " + timeQuantumField + "\n")
	}
	b.WriteString("attributes:\n")
	b.WriteString("  - fieldName: o_orderkey\n")
	b.WriteString("    sourceName: /data/o_orderkey\n")
	b.WriteString("    mappingStrategy: IntBSI\n")
	b.WriteString("    type: Integer\n")
	b.WriteString("    sourceOrdinal: 1\n")
	b.WriteString("    columnID: true\n")
	b.WriteString("  - fieldName: o_clerk\n")
	b.WriteString("    sourceName: /data/o_clerk\n")
	b.WriteString("    mappingStrategy: " + clerkMapping + "\n")
	b.WriteString("    type: String\n")
	if clerkMapping == "StringLexBSI" {
		b.WriteString("    configuration:\n")
		b.WriteString("      length: \"15\"\n")
	}
	b.WriteString("    sourceOrdinal: 2\n")
	b.WriteString("  - fieldName: o_orderdate\n")
	b.WriteString("    sourceName: /data/o_orderdate\n")
	b.WriteString("    mappingStrategy: TimestampBSI\n")
	b.WriteString("    configuration:\n")
	b.WriteString("      granularity: second\n")
	b.WriteString("    type: " + dateType + "\n")
	b.WriteString("    sourceOrdinal: 3\n")
	return b.String()
}

func assertDifference(t *testing.T, result Result, code, table, field, property string) {
	t.Helper()
	for _, diff := range result.Differences {
		if diff.Code == code && diff.Table == table && diff.Field == field && diff.Property == property {
			return
		}
	}
	t.Fatalf("difference %s/%s/%s/%s not found in %+v", code, table, field, property, result.Differences)
}
