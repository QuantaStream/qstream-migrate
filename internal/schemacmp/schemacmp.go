package schemacmp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Difference struct {
	Code      string
	Table     string
	Field     string
	Property  string
	Reference string
	Generated string
	Detail    string
}

type Result struct {
	Differences    []Difference
	TablesCompared int
}

func (r Result) Match() bool {
	return len(r.Differences) == 0
}

func (r Result) Status() string {
	if r.Match() {
		return "MATCH"
	}
	return "DIFF"
}

func CompareDirs(referenceDir, generatedDir string) (Result, error) {
	reference, err := loadSchemas(referenceDir)
	if err != nil {
		return Result{}, fmt.Errorf("load reference schemas: %w", err)
	}
	generated, err := loadSchemas(generatedDir)
	if err != nil {
		return Result{}, fmt.Errorf("load generated schemas: %w", err)
	}

	var result Result
	tables := unionKeys(reference, generated)
	for _, table := range tables {
		ref, refOK := reference[table]
		gen, genOK := generated[table]
		switch {
		case !refOK:
			result.add(Difference{Code: "extra_table", Table: table, Detail: "Generated schema contains a table missing from reference."})
			continue
		case !genOK:
			result.add(Difference{Code: "missing_table", Table: table, Detail: "Generated schema is missing a reference table."})
			continue
		}
		result.TablesCompared++
		compareSchema(&result, ref, gen)
	}
	return result, nil
}

func FormatDifferences(result Result) []string {
	lines := make([]string, 0, len(result.Differences))
	for _, diff := range result.Differences {
		parts := []string{
			"schema_compare",
			"DIFF",
			"code=" + diff.Code,
		}
		if diff.Table != "" {
			parts = append(parts, "table="+diff.Table)
		}
		if diff.Field != "" {
			parts = append(parts, "field="+diff.Field)
		}
		if diff.Property != "" {
			parts = append(parts, "property="+diff.Property)
		}
		if diff.Reference != "" {
			parts = append(parts, "reference="+quote(diff.Reference))
		}
		if diff.Generated != "" {
			parts = append(parts, "generated="+quote(diff.Generated))
		}
		if diff.Detail != "" {
			parts = append(parts, "detail="+quote(diff.Detail))
		}
		lines = append(lines, strings.Join(parts, " "))
	}
	sort.Strings(lines)
	return lines
}

type schema struct {
	TableName        string      `yaml:"tableName"`
	PrimaryKey       string      `yaml:"primaryKey"`
	Selector         string      `yaml:"selector"`
	TimeQuantumType  string      `yaml:"timeQuantumType"`
	TimeQuantumField string      `yaml:"timeQuantumField"`
	Attributes       []attribute `yaml:"attributes"`
}

type attribute struct {
	FieldName             string         `yaml:"fieldName"`
	SourceName            string         `yaml:"sourceName"`
	MappingStrategy       string         `yaml:"mappingStrategy"`
	Configuration         map[string]any `yaml:"configuration"`
	Type                  string         `yaml:"type"`
	MaxLen                *int64         `yaml:"maxLen"`
	Scale                 *int64         `yaml:"scale"`
	Searchable            *bool          `yaml:"searchable"`
	ForeignKey            string         `yaml:"foreignKey"`
	RelationshipArtifacts map[string]any `yaml:"relationshipArtifacts"`
	SourceOrdinal         int            `yaml:"sourceOrdinal"`
	ColumnID              bool           `yaml:"columnID"`
}

func loadSchemas(root string) (map[string]schema, error) {
	paths, err := filepath.Glob(filepath.Join(root, "*", "schema.yaml"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no schema files found under %s", root)
	}

	schemas := make(map[string]schema, len(paths))
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var parsed schema
		if err := yaml.Unmarshal(payload, &parsed); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if parsed.TableName == "" {
			parsed.TableName = filepath.Base(filepath.Dir(path))
		}
		if parsed.TableName == "" {
			return nil, fmt.Errorf("schema %s has no tableName", path)
		}
		if _, exists := schemas[parsed.TableName]; exists {
			return nil, fmt.Errorf("duplicate schema for table %s", parsed.TableName)
		}
		schemas[parsed.TableName] = parsed
	}
	return schemas, nil
}

func compareSchema(result *Result, ref, gen schema) {
	table := ref.TableName
	compareProperty(result, table, "", "tableName", ref.TableName, gen.TableName)
	compareProperty(result, table, "", "primaryKey", ref.PrimaryKey, gen.PrimaryKey)
	compareProperty(result, table, "", "selector", ref.Selector, gen.Selector)
	compareProperty(result, table, "", "timeQuantumType", ref.TimeQuantumType, gen.TimeQuantumType)
	compareProperty(result, table, "", "timeQuantumField", ref.TimeQuantumField, gen.TimeQuantumField)

	refAttrs := attributesByName(ref.Attributes)
	genAttrs := attributesByName(gen.Attributes)
	for _, field := range unionKeys(refAttrs, genAttrs) {
		refAttr, refOK := refAttrs[field]
		genAttr, genOK := genAttrs[field]
		switch {
		case !refOK:
			result.add(Difference{Code: "extra_field", Table: table, Field: field, Detail: "Generated schema contains a field missing from reference."})
			continue
		case !genOK:
			result.add(Difference{Code: "missing_field", Table: table, Field: field, Detail: "Generated schema is missing a reference field."})
			continue
		}
		compareAttribute(result, table, refAttr, genAttr)
	}
	compareFieldOrder(result, table, ref.Attributes, gen.Attributes)
}

func compareAttribute(result *Result, table string, ref, gen attribute) {
	field := ref.FieldName
	compareProperty(result, table, field, "sourceName", ref.SourceName, gen.SourceName)
	compareProperty(result, table, field, "mappingStrategy", ref.MappingStrategy, gen.MappingStrategy)
	compareProperty(result, table, field, "type", ref.Type, gen.Type)
	compareProperty(result, table, field, "foreignKey", ref.ForeignKey, gen.ForeignKey)
	compareProperty(result, table, field, "maxLen", ptrInt64(ref.MaxLen), ptrInt64(gen.MaxLen))
	compareProperty(result, table, field, "scale", ptrInt64(ref.Scale), ptrInt64(gen.Scale))
	compareProperty(result, table, field, "searchable", ptrBool(ref.Searchable), ptrBool(gen.Searchable))
	compareProperty(result, table, field, "sourceOrdinal", zeroEmpty(ref.SourceOrdinal), zeroEmpty(gen.SourceOrdinal))
	compareProperty(result, table, field, "columnID", falseEmpty(ref.ColumnID), falseEmpty(gen.ColumnID))
	compareConfig(result, table, field, "configuration", ref.Configuration, gen.Configuration)
	compareConfig(result, table, field, "relationshipArtifacts", ref.RelationshipArtifacts, gen.RelationshipArtifacts)
}

func compareFieldOrder(result *Result, table string, ref, gen []attribute) {
	refOrder := fieldOrder(ref)
	genOrder := fieldOrder(gen)
	if strings.Join(refOrder, "\x00") == strings.Join(genOrder, "\x00") {
		return
	}
	result.add(Difference{
		Code:      "field_order",
		Table:     table,
		Reference: strings.Join(refOrder, ","),
		Generated: strings.Join(genOrder, ","),
		Detail:    "Field order differs.",
	})
}

func compareConfig(result *Result, table, field, prefix string, ref, gen map[string]any) {
	for _, key := range unionKeys(ref, gen) {
		compareProperty(result, table, field, prefix+"."+key, scalar(ref[key]), scalar(gen[key]))
	}
}

func compareProperty(result *Result, table, field, property, ref, gen string) {
	if ref == gen {
		return
	}
	result.add(Difference{
		Code:      "property_diff",
		Table:     table,
		Field:     field,
		Property:  property,
		Reference: ref,
		Generated: gen,
	})
}

func attributesByName(attrs []attribute) map[string]attribute {
	out := make(map[string]attribute, len(attrs))
	for _, attr := range attrs {
		out[attr.FieldName] = attr
	}
	return out
}

func fieldOrder(attrs []attribute) []string {
	out := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, attr.FieldName)
	}
	return out
}

func unionKeys[V any](left, right map[string]V) []string {
	seen := map[string]struct{}{}
	for key := range left {
		seen[key] = struct{}{}
	}
	for key := range right {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func ptrInt64(value *int64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(*value)
}

func ptrBool(value *bool) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(*value)
}

func zeroEmpty(value int) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprint(value)
}

func falseEmpty(value bool) string {
	if !value {
		return ""
	}
	return "true"
}

func scalar(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func quote(value string) string {
	return fmt.Sprintf("%q", value)
}

func (r *Result) add(diff Difference) {
	r.Differences = append(r.Differences, diff)
}
