package analyze

import (
	"strings"
	"time"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

type Options struct {
	StringEnumMaxDistinct  int64
	DefaultLexPrefixLength int
}

func BuildPlan(inv model.Inventory, opts Options) model.MigrationPlan {
	if opts.StringEnumMaxDistinct <= 0 {
		opts.StringEnumMaxDistinct = 500
	}
	if opts.DefaultLexPrefixLength <= 0 {
		opts.DefaultLexPrefixLength = 16
	}

	plan := model.MigrationPlan{
		Version:     1,
		GeneratedAt: time.Now().UTC(),
		Source:      inv.Source,
		Settings: model.AnalyzerSettings{
			StringEnumMaxDistinct:  opts.StringEnumMaxDistinct,
			DefaultLexPrefixLength: opts.DefaultLexPrefixLength,
		},
		Notes: []string{
			"Review this plan before generating QuantaStream schema files.",
			"Mapping recommendations are based on MySQL metadata plus lightweight profiling.",
			"Free-text fields and relationship choices should be reviewed by a human.",
		},
	}

	for _, table := range inv.Tables {
		tablePlan := model.TablePlan{
			Name:       table.Name,
			SourceName: table.Name,
			Include:    true,
			PrimaryKey: append([]string(nil), table.PrimaryKey...),
			RowCount:   tableRowCount(table),
		}
		if len(table.PrimaryKey) == 0 {
			tablePlan.Notes = append(tablePlan.Notes, "No primary key was found in MySQL metadata; choose a stable QuantaStream primary key before loading.")
		}
		for _, column := range table.Columns {
			tablePlan.Fields = append(tablePlan.Fields, recommendField(table, column, opts))
		}
		tablePlan.Relationships = append(tablePlan.Relationships, metadataRelationships(table)...)
		tablePlan.Relationships = append(tablePlan.Relationships, candidateRelationships(inv, table)...)
		plan.Tables = append(plan.Tables, tablePlan)
	}
	return plan
}

func metadataRelationships(table model.TableInventory) []model.RelationshipPlan {
	relationships := make([]model.RelationshipPlan, 0, len(table.ForeignKeys))
	for _, fk := range table.ForeignKeys {
		relationships = append(relationships, model.RelationshipPlan{
			Name:          fk.Name,
			Kind:          "foreign_key",
			Confidence:    "metadata",
			Columns:       append([]string(nil), fk.Columns...),
			ParentTable:   fk.ParentTable,
			ParentColumns: append([]string(nil), fk.ParentColumns...),
		})
	}
	return relationships
}

func candidateRelationships(inv model.Inventory, child model.TableInventory) []model.RelationshipPlan {
	var candidates []model.RelationshipPlan
	for _, column := range child.Columns {
		if isForeignKeyColumn(child, column.Name) || isSingleColumnPrimaryKey(child, column.Name) {
			continue
		}
		for _, parent := range inv.Tables {
			if parent.Name == child.Name || len(parent.PrimaryKey) != 1 {
				continue
			}
			parentPK := parent.PrimaryKey[0]
			parentColumn, ok := columnByName(parent, parentPK)
			if !ok || !columnTypesCompatible(column, parentColumn) {
				continue
			}
			if !columnLooksLikeReference(column.Name, parent.Name, parentPK) {
				continue
			}
			candidates = append(candidates, model.RelationshipPlan{
				Name:          "candidate_" + child.Name + "_" + column.Name + "_to_" + parent.Name + "_" + parentPK,
				Kind:          "candidate_foreign_key",
				Confidence:    "name_and_type_match",
				Columns:       []string{column.Name},
				ParentTable:   parent.Name,
				ParentColumns: []string{parentPK},
				Warnings: []string{
					"Name-based relationship candidate only; validate parent coverage before generating QuantaStream relationship schema.",
				},
			})
		}
	}
	return candidates
}

func columnLooksLikeReference(columnName, parentTable, parentPK string) bool {
	column := strings.ToLower(columnName)
	table := strings.ToLower(parentTable)
	singular := strings.TrimSuffix(table, "s")
	pk := strings.ToLower(parentPK)
	normalizedColumn := normalizeKeyName(column)
	normalizedPK := normalizeKeyName(pk)

	return (pk != "id" && pk != "key" && column == pk) ||
		(normalizedPK != "" && normalizedPK != "id" && normalizedColumn == normalizedPK) ||
		column == table+"_"+pk ||
		column == singular+"_"+pk ||
		column == table+"_key" ||
		column == singular+"_key"
}

func normalizeKeyName(name string) string {
	lower := strings.ToLower(name)
	parts := strings.Split(lower, "_")
	if len(parts) > 1 {
		lower = parts[len(parts)-1]
	}
	for _, prefix := range []string{"pk", "fk"} {
		lower = strings.TrimPrefix(lower, prefix+"_")
	}
	return lower
}

func columnByName(table model.TableInventory, name string) (model.ColumnInventory, bool) {
	for _, column := range table.Columns {
		if column.Name == name {
			return column, true
		}
	}
	return model.ColumnInventory{}, false
}

func columnTypesCompatible(left, right model.ColumnInventory) bool {
	leftType := strings.ToLower(left.DataType)
	rightType := strings.ToLower(right.DataType)
	if leftType == rightType {
		return true
	}
	if isIntegerType(leftType) && isIntegerType(rightType) {
		return true
	}
	if isStringType(leftType) && isStringType(rightType) {
		return true
	}
	return false
}

func tableRowCount(table model.TableInventory) int64 {
	for _, column := range table.Columns {
		if column.Profile != nil && column.Profile.RowCount > 0 {
			return column.Profile.RowCount
		}
	}
	if table.RowEstimate != nil {
		return *table.RowEstimate
	}
	return 0
}

func recommendField(table model.TableInventory, column model.ColumnInventory, opts Options) model.FieldPlan {
	field := model.FieldPlan{
		Name:          column.Name,
		SourceName:    column.Name,
		Include:       true,
		SourceType:    column.ColumnType,
		Nullable:      column.Nullable,
		SourceOrdinal: column.Ordinal,
	}

	dataType := strings.ToLower(column.DataType)
	switch {
	case isIntegerType(dataType):
		field.RecommendedMappingStrategy = "IntBSI"
		field.QuantaStreamType = "Integer"
		field.Rationale = append(field.Rationale, "MySQL integer type.")
	case isFloatType(dataType):
		field.RecommendedMappingStrategy = "FloatScaleBSI"
		field.QuantaStreamType = "Float"
		field.Rationale = append(field.Rationale, "MySQL floating-point numeric type.")
	case isDecimalType(dataType):
		field.RecommendedMappingStrategy = "FloatScaleBSI"
		field.QuantaStreamType = "Float"
		if column.NumericScale != nil {
			field.Scale = column.NumericScale
		}
		field.Rationale = append(field.Rationale, "MySQL fixed-scale decimal type.")
	case isDateType(dataType):
		field.RecommendedMappingStrategy = "TimestampBSI"
		field.QuantaStreamType = "Date"
		field.Rationale = append(field.Rationale, "MySQL date/time type.")
	case isStringType(dataType):
		applyStringRecommendation(table, column, opts, &field)
	default:
		field.RecommendedMappingStrategy = "Review"
		field.QuantaStreamType = "String"
		field.Warnings = append(field.Warnings, "No default QuantaStream mapping rule for MySQL type "+column.ColumnType+".")
	}

	if isSingleColumnPrimaryKey(table, column.Name) {
		field.ColumnID = true
		field.Rationale = append(field.Rationale, "Single-column primary key.")
	}
	return field
}

func applyStringRecommendation(table model.TableInventory, column model.ColumnInventory, opts Options, field *model.FieldPlan) {
	field.QuantaStreamType = "String"
	maxLen := observedOrDeclaredMaxLen(column)
	if maxLen > 0 {
		field.MaxLen = &maxLen
	}

	distinct := int64(0)
	hasDistinct := false
	rowCount := int64(0)
	if column.Profile != nil {
		rowCount = column.Profile.RowCount
		if column.Profile.DistinctCount != nil {
			distinct = *column.Profile.DistinctCount
			hasDistinct = true
		}
	}

	isKeyLike := isSingleColumnPrimaryKey(table, column.Name) || isForeignKeyColumn(table, column.Name) || isUniqueIndexedColumn(table, column.Name) || looksLikeIdentifier(column.Name)
	isLongText := isTextType(column.DataType) || maxLen > 512
	isHighCardinality := hasDistinct && distinct > opts.StringEnumMaxDistinct
	isMostlyUnique := hasDistinct && rowCount > 0 && float64(distinct)/float64(rowCount) > 0.50

	switch {
	case isLongText:
		field.RecommendedMappingStrategy = "ReviewText"
		field.Rationale = append(field.Rationale, "Long or text-like string field.")
		field.Warnings = append(field.Warnings, "Review whether this should be searchable text, omitted, or modeled as a dimension.")
	case isKeyLike || isHighCardinality || isMostlyUnique:
		field.RecommendedMappingStrategy = "StringLexBSI"
		prefix := opts.DefaultLexPrefixLength
		if maxLen > 0 && maxLen < int64(prefix) {
			prefix = int(maxLen)
		}
		if prefix < 1 {
			prefix = opts.DefaultLexPrefixLength
		}
		field.LexPrefixLength = &prefix
		if isKeyLike {
			field.Rationale = append(field.Rationale, "Identifier, unique-indexed, primary-key, or foreign-key-like string.")
		}
		if isHighCardinality {
			field.Rationale = append(field.Rationale, "Distinct count exceeds StringEnum threshold.")
		}
		if isMostlyUnique {
			field.Rationale = append(field.Rationale, "String values are mostly unique.")
		}
	default:
		field.RecommendedMappingStrategy = "StringEnum"
		if hasDistinct {
			field.Rationale = append(field.Rationale, "Distinct count fits StringEnum threshold.")
		} else {
			field.Rationale = append(field.Rationale, "No high-cardinality or identifier signal found.")
		}
	}
}

func observedOrDeclaredMaxLen(column model.ColumnInventory) int64 {
	if column.Profile != nil && column.Profile.MaxStringLength != nil {
		return *column.Profile.MaxStringLength
	}
	if column.CharacterMaximumLength != nil {
		return *column.CharacterMaximumLength
	}
	return 0
}

func isIntegerType(dataType string) bool {
	switch dataType {
	case "bit", "bool", "boolean", "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "year":
		return true
	default:
		return false
	}
}

func isFloatType(dataType string) bool {
	switch dataType {
	case "float", "double", "real":
		return true
	default:
		return false
	}
}

func isDecimalType(dataType string) bool {
	switch dataType {
	case "decimal", "dec", "numeric", "fixed":
		return true
	default:
		return false
	}
}

func isDateType(dataType string) bool {
	switch dataType {
	case "date", "datetime", "timestamp", "time":
		return true
	default:
		return false
	}
}

func isStringType(dataType string) bool {
	switch strings.ToLower(dataType) {
	case "char", "varchar", "binary", "varbinary", "tinytext", "text", "mediumtext", "longtext", "enum", "set":
		return true
	default:
		return false
	}
}

func isTextType(dataType string) bool {
	switch strings.ToLower(dataType) {
	case "tinytext", "text", "mediumtext", "longtext":
		return true
	default:
		return false
	}
}

func isSingleColumnPrimaryKey(table model.TableInventory, column string) bool {
	return len(table.PrimaryKey) == 1 && table.PrimaryKey[0] == column
}

func isForeignKeyColumn(table model.TableInventory, column string) bool {
	for _, fk := range table.ForeignKeys {
		for _, fkColumn := range fk.Columns {
			if fkColumn == column {
				return true
			}
		}
	}
	return false
}

func isUniqueIndexedColumn(table model.TableInventory, column string) bool {
	for _, index := range table.Indexes {
		if !index.Unique {
			continue
		}
		for _, indexColumn := range index.Columns {
			if indexColumn.Name == column {
				return true
			}
		}
	}
	return false
}

func looksLikeIdentifier(name string) bool {
	lower := strings.ToLower(name)
	return lower == "id" ||
		strings.HasSuffix(lower, "_id") ||
		strings.HasSuffix(lower, "id") ||
		strings.HasSuffix(lower, "_key") ||
		strings.HasSuffix(lower, "key") ||
		strings.Contains(lower, "uuid") ||
		strings.Contains(lower, "guid") ||
		strings.Contains(lower, "code")
}
