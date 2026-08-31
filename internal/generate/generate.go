package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/QuantaStream/qstream-migrate/internal/model"
	"gopkg.in/yaml.v3"
)

type Options struct {
	OutDir               string
	RelationshipMode     string
	SourceRoot           string
	TimestampGranularity string
	DefaultLexLength     int
	Overwrite            bool
}

type Result struct {
	Files []string
}

type qstreamSchema struct {
	TableName  string             `yaml:"tableName"`
	PrimaryKey string             `yaml:"primaryKey"`
	Selector   string             `yaml:"selector"`
	Attributes []qstreamAttribute `yaml:"attributes"`
}

type qstreamAttribute struct {
	FieldName             string                        `yaml:"fieldName"`
	SourceName            string                        `yaml:"sourceName"`
	MappingStrategy       string                        `yaml:"mappingStrategy"`
	Configuration         map[string]string             `yaml:"configuration,omitempty"`
	Type                  string                        `yaml:"type"`
	MaxLen                *int64                        `yaml:"maxLen,omitempty"`
	Scale                 *int64                        `yaml:"scale,omitempty"`
	ForeignKey            string                        `yaml:"foreignKey,omitempty"`
	RelationshipArtifacts *qstreamRelationshipArtifacts `yaml:"relationshipArtifacts,omitempty"`
	SourceOrdinal         int                           `yaml:"sourceOrdinal"`
	ColumnID              bool                          `yaml:"columnID,omitempty"`
}

type qstreamRelationshipArtifacts struct {
	ParentToChild bool `yaml:"parentToChild"`
}

func WriteSchemas(plan model.MigrationPlan, opts Options) (Result, error) {
	opts = normalizeOptions(opts, plan)
	if err := validateRelationshipMode(opts.RelationshipMode); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create output directory: %w", err)
	}

	var result Result
	for _, table := range plan.Tables {
		if !table.Include {
			continue
		}
		schema, err := buildSchema(table, opts)
		if err != nil {
			return Result{}, err
		}
		tableDir := filepath.Join(opts.OutDir, table.Name)
		if err := os.MkdirAll(tableDir, 0o755); err != nil {
			return Result{}, fmt.Errorf("create table directory %s: %w", tableDir, err)
		}
		path := filepath.Join(tableDir, "schema.yaml")
		if !opts.Overwrite {
			if _, err := os.Stat(path); err == nil {
				return Result{}, fmt.Errorf("schema already exists: %s", path)
			} else if !os.IsNotExist(err) {
				return Result{}, fmt.Errorf("stat schema %s: %w", path, err)
			}
		}
		payload, err := yaml.Marshal(schema)
		if err != nil {
			return Result{}, fmt.Errorf("encode schema for table %s: %w", table.Name, err)
		}
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			return Result{}, fmt.Errorf("write schema %s: %w", path, err)
		}
		result.Files = append(result.Files, path)
	}
	return result, nil
}

func normalizeOptions(opts Options, plan model.MigrationPlan) Options {
	if opts.OutDir == "" {
		opts.OutDir = "configuration"
	}
	if opts.RelationshipMode == "" {
		opts.RelationshipMode = "metadata"
	}
	if opts.SourceRoot == "" {
		opts.SourceRoot = "/data"
	}
	if opts.TimestampGranularity == "" {
		opts.TimestampGranularity = "second"
	}
	if opts.DefaultLexLength <= 0 {
		opts.DefaultLexLength = plan.Settings.DefaultLexPrefixLength
	}
	if opts.DefaultLexLength <= 0 {
		opts.DefaultLexLength = 16
	}
	return opts
}

func validateRelationshipMode(mode string) error {
	switch mode {
	case "metadata", "all", "none":
		return nil
	default:
		return fmt.Errorf("unsupported relationship mode %q; use metadata, all, or none", mode)
	}
}

func buildSchema(table model.TablePlan, opts Options) (qstreamSchema, error) {
	if table.Name == "" {
		return qstreamSchema{}, fmt.Errorf("table has no name")
	}
	if len(table.PrimaryKey) == 0 {
		return qstreamSchema{}, fmt.Errorf("table %s has no primary_key; edit the plan before generating schema", table.Name)
	}

	relationships, err := relationshipsByColumn(table, opts.RelationshipMode)
	if err != nil {
		return qstreamSchema{}, err
	}

	schema := qstreamSchema{
		TableName:  table.Name,
		PrimaryKey: strings.Join(table.PrimaryKey, "+"),
		Selector:   fmt.Sprintf("type=%q", table.Name),
	}
	for _, field := range table.Fields {
		if !field.Include {
			continue
		}
		attr, err := buildAttribute(field, relationships[field.Name], opts)
		if err != nil {
			return qstreamSchema{}, fmt.Errorf("table %s field %s: %w", table.Name, field.Name, err)
		}
		schema.Attributes = append(schema.Attributes, attr)
	}
	if len(schema.Attributes) == 0 {
		return qstreamSchema{}, fmt.Errorf("table %s has no included fields", table.Name)
	}
	return schema, nil
}

func relationshipsByColumn(table model.TablePlan, mode string) (map[string]model.RelationshipPlan, error) {
	relationships := make(map[string]model.RelationshipPlan)
	if mode == "none" {
		return relationships, nil
	}
	for _, relationship := range table.Relationships {
		if !includeRelationship(relationship, mode) {
			continue
		}
		if len(relationship.Columns) != 1 {
			return nil, fmt.Errorf("table %s relationship %s uses %d columns; only single-column ParentRelation generation is supported in this slice", table.Name, relationship.Name, len(relationship.Columns))
		}
		column := relationship.Columns[0]
		if existing, ok := relationships[column]; ok {
			return nil, fmt.Errorf("table %s column %s has multiple relationships: %s and %s", table.Name, column, existing.Name, relationship.Name)
		}
		relationships[column] = relationship
	}
	return relationships, nil
}

func includeRelationship(relationship model.RelationshipPlan, mode string) bool {
	switch mode {
	case "all":
		return relationship.Kind == "foreign_key" || relationship.Kind == "candidate_foreign_key"
	case "metadata":
		return relationship.Kind == "foreign_key"
	default:
		return false
	}
}

func buildAttribute(field model.FieldPlan, relationship model.RelationshipPlan, opts Options) (qstreamAttribute, error) {
	if field.Name == "" {
		return qstreamAttribute{}, fmt.Errorf("field has no name")
	}
	if field.SourceOrdinal <= 0 {
		return qstreamAttribute{}, fmt.Errorf("source_ordinal must be greater than zero")
	}

	mapping := field.RecommendedMappingStrategy
	attr := qstreamAttribute{
		FieldName:       field.Name,
		SourceName:      sourcePath(opts.SourceRoot, field.SourceName),
		MappingStrategy: mapping,
		Type:            field.QuantaStreamType,
		Scale:           field.Scale,
		SourceOrdinal:   field.SourceOrdinal,
		ColumnID:        field.ColumnID,
	}
	if attr.Type == "" {
		attr.Type = "String"
	}

	if relationship.Name != "" {
		attr.MappingStrategy = "ParentRelation"
		attr.ForeignKey = relationship.ParentTable
		attr.RelationshipArtifacts = &qstreamRelationshipArtifacts{ParentToChild: true}
		return attr, nil
	}

	switch mapping {
	case "IntBSI", "StringEnum", "FloatScaleBSI":
		return attr, nil
	case "TimestampBSI":
		attr.Configuration = map[string]string{"granularity": opts.TimestampGranularity}
		return attr, nil
	case "StringLexBSI":
		length := opts.DefaultLexLength
		if field.LexPrefixLength != nil && *field.LexPrefixLength > 0 {
			length = *field.LexPrefixLength
		}
		attr.Configuration = map[string]string{"length": strconv.Itoa(length)}
		attr.MaxLen = field.MaxLen
		return attr, nil
	case "Review", "ReviewText", "":
		return qstreamAttribute{}, fmt.Errorf("mapping %q requires plan review before schema generation", mapping)
	default:
		return qstreamAttribute{}, fmt.Errorf("unsupported mapping strategy %q", mapping)
	}
}

func sourcePath(root, sourceName string) string {
	if strings.HasPrefix(sourceName, "/") {
		return sourceName
	}
	if sourceName == "" {
		return root
	}
	return strings.TrimRight(root, "/") + "/" + sourceName
}
