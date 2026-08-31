package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

type Options struct {
	RelationshipMode string
	Strict           bool
}

type Severity string

const (
	SeverityError Severity = "ERROR"
	SeverityWarn  Severity = "WARN"
)

type Issue struct {
	Severity     Severity
	Code         string
	Table        string
	Field        string
	Relationship string
	Detail       string
}

type Result struct {
	Issues   []Issue
	Tables   int
	Fields   int
	Errors   int
	Warnings int
}

func CheckPlan(plan model.MigrationPlan, opts Options) Result {
	if opts.RelationshipMode == "" {
		opts.RelationshipMode = "metadata"
	}

	tableNames := includedTableNames(plan)
	result := Result{Tables: len(tableNames)}
	if result.Tables == 0 {
		result.add(Issue{
			Severity: SeverityError,
			Code:     "no_included_tables",
			Detail:   "Plan has no included tables.",
		})
	}

	for _, table := range plan.Tables {
		if !table.Include {
			continue
		}
		checkTable(&result, table, tableNames, opts)
	}
	return result
}

func (r *Result) Pass(strict bool) bool {
	if r.Errors > 0 {
		return false
	}
	return !strict || r.Warnings == 0
}

func (r *Result) Status(strict bool) string {
	if r.Pass(strict) {
		return "PASS"
	}
	return "FAIL"
}

func (r *Result) add(issue Issue) {
	r.Issues = append(r.Issues, issue)
	switch issue.Severity {
	case SeverityError:
		r.Errors++
	case SeverityWarn:
		r.Warnings++
	}
}

func checkTable(result *Result, table model.TablePlan, tableNames map[string]struct{}, opts Options) {
	if strings.TrimSpace(table.Name) == "" {
		result.add(Issue{
			Severity: SeverityError,
			Code:     "missing_table_name",
			Detail:   "Included table has no name.",
		})
		return
	}

	fields := includedFieldsByName(table)
	result.Fields += len(fields)
	if len(fields) == 0 {
		result.add(Issue{
			Severity: SeverityError,
			Code:     "no_included_fields",
			Table:    table.Name,
			Detail:   "Included table has no included fields.",
		})
	}
	checkPrimaryKey(result, table, fields)
	checkFields(result, table)
	checkTimeQuantum(result, table, fields)
	checkRelationships(result, table, tableNames, fields, opts)
}

func includedFieldsByName(table model.TablePlan) map[string]model.FieldPlan {
	fields := make(map[string]model.FieldPlan)
	for _, field := range table.Fields {
		if field.Include {
			fields[field.Name] = field
		}
	}
	return fields
}

func includedTableNames(plan model.MigrationPlan) map[string]struct{} {
	names := make(map[string]struct{})
	for _, table := range plan.Tables {
		if table.Include && strings.TrimSpace(table.Name) != "" {
			names[table.Name] = struct{}{}
		}
	}
	return names
}

func checkPrimaryKey(result *Result, table model.TablePlan, fields map[string]model.FieldPlan) {
	if len(table.PrimaryKey) == 0 {
		result.add(Issue{
			Severity: SeverityError,
			Code:     "missing_primary_key",
			Table:    table.Name,
			Detail:   "Included table must choose a primary key before schema generation.",
		})
		return
	}
	for _, pk := range table.PrimaryKey {
		if _, ok := fields[pk]; !ok {
			result.add(Issue{
				Severity: SeverityError,
				Code:     "primary_key_field_not_included",
				Table:    table.Name,
				Field:    pk,
				Detail:   "Primary-key field is missing or not included.",
			})
		}
	}
}

func checkFields(result *Result, table model.TablePlan) {
	seen := map[string]struct{}{}
	for _, field := range table.Fields {
		if !field.Include {
			continue
		}
		if strings.TrimSpace(field.Name) == "" {
			result.add(Issue{
				Severity: SeverityError,
				Code:     "missing_field_name",
				Table:    table.Name,
				Detail:   "Included field has no name.",
			})
			continue
		}
		if _, ok := seen[field.Name]; ok {
			result.add(Issue{
				Severity: SeverityError,
				Code:     "duplicate_field_name",
				Table:    table.Name,
				Field:    field.Name,
				Detail:   "Included field name is duplicated.",
			})
		}
		seen[field.Name] = struct{}{}

		switch field.RecommendedMappingStrategy {
		case "IntBSI", "StringEnum", "StringLexBSI", "FloatScaleBSI", "TimestampBSI":
		case "Review", "ReviewText", "":
			result.add(Issue{
				Severity: SeverityError,
				Code:     "unresolved_mapping",
				Table:    table.Name,
				Field:    field.Name,
				Detail:   fmt.Sprintf("Mapping %q must be reviewed before schema generation.", field.RecommendedMappingStrategy),
			})
		default:
			result.add(Issue{
				Severity: SeverityError,
				Code:     "unsupported_mapping",
				Table:    table.Name,
				Field:    field.Name,
				Detail:   fmt.Sprintf("Mapping %q is not supported by schema generation.", field.RecommendedMappingStrategy),
			})
		}
		if strings.TrimSpace(field.SourceName) == "" {
			result.add(Issue{
				Severity: SeverityWarn,
				Code:     "missing_source_name",
				Table:    table.Name,
				Field:    field.Name,
				Detail:   "Source name is empty; generated sourceName may need review.",
			})
		}
		if field.SourceOrdinal <= 0 {
			result.add(Issue{
				Severity: SeverityError,
				Code:     "invalid_source_ordinal",
				Table:    table.Name,
				Field:    field.Name,
				Detail:   "source_ordinal must be greater than zero.",
			})
		}
	}
}

func checkTimeQuantum(result *Result, table model.TablePlan, fields map[string]model.FieldPlan) {
	if table.TimeQuantum == nil {
		return
	}
	field := strings.TrimSpace(table.TimeQuantum.Field)
	quantumType := strings.TrimSpace(table.TimeQuantum.Type)
	if field == "" {
		result.add(Issue{
			Severity: SeverityWarn,
			Code:     "time_quantum_unselected",
			Table:    table.Name,
			Detail:   "Date/time fields were found; set time_quantum.field when this table should be time partitioned.",
		})
		return
	}
	if quantumType == "" {
		result.add(Issue{
			Severity: SeverityError,
			Code:     "time_quantum_type_missing",
			Table:    table.Name,
			Field:    field,
			Detail:   "time_quantum.type is required when time_quantum.field is set.",
		})
	}
	if _, ok := fields[field]; !ok {
		result.add(Issue{
			Severity: SeverityError,
			Code:     "time_quantum_field_not_included",
			Table:    table.Name,
			Field:    field,
			Detail:   "time_quantum.field must reference an included field.",
		})
	}
}

func checkRelationships(result *Result, table model.TablePlan, tableNames map[string]struct{}, fields map[string]model.FieldPlan, opts Options) {
	included := includedRelationships(table.Relationships, opts.RelationshipMode)
	skippedCandidates := skippedCandidateCount(table.Relationships, opts.RelationshipMode)
	if opts.RelationshipMode == "metadata" && skippedCandidates > 0 {
		result.add(Issue{
			Severity: SeverityWarn,
			Code:     "candidate_relationships_skipped",
			Table:    table.Name,
			Detail:   fmt.Sprintf("%d name-based relationship candidate(s) will not be generated unless --relationship-mode all is used.", skippedCandidates),
		})
	}
	if opts.RelationshipMode == "none" && len(table.Relationships) > 0 {
		result.add(Issue{
			Severity: SeverityWarn,
			Code:     "relationships_disabled",
			Table:    table.Name,
			Detail:   fmt.Sprintf("%d relationship(s) or candidate(s) will not be generated in relationship-mode none.", len(table.Relationships)),
		})
	}
	for _, relationship := range included {
		if relationship.Kind == "candidate_foreign_key" {
			result.add(Issue{
				Severity:     SeverityWarn,
				Code:         "candidate_relationship_enabled",
				Table:        table.Name,
				Relationship: relationship.Name,
				Detail:       "Name-based relationship candidate will be generated; validate parent coverage before loading.",
			})
		}
		if len(relationship.Columns) != 1 {
			result.add(Issue{
				Severity:     SeverityError,
				Code:         "multi_column_relationship",
				Table:        table.Name,
				Relationship: relationship.Name,
				Detail:       "Schema generation currently supports single-column ParentRelation fields only.",
			})
			continue
		}
		column := relationship.Columns[0]
		if _, ok := fields[column]; !ok {
			result.add(Issue{
				Severity:     SeverityError,
				Code:         "relationship_field_not_included",
				Table:        table.Name,
				Field:        column,
				Relationship: relationship.Name,
				Detail:       "Relationship column must reference an included field.",
			})
		}
		if _, ok := tableNames[relationship.ParentTable]; !ok {
			result.add(Issue{
				Severity:     SeverityError,
				Code:         "relationship_parent_not_included",
				Table:        table.Name,
				Relationship: relationship.Name,
				Detail:       fmt.Sprintf("Parent table %q is missing or not included.", relationship.ParentTable),
			})
		}
		if len(relationship.ParentColumns) == 0 {
			result.add(Issue{
				Severity:     SeverityError,
				Code:         "relationship_parent_column_missing",
				Table:        table.Name,
				Relationship: relationship.Name,
				Detail:       "Relationship must name at least one parent column.",
			})
		}
	}
}

func includedRelationships(relationships []model.RelationshipPlan, mode string) []model.RelationshipPlan {
	var included []model.RelationshipPlan
	for _, relationship := range relationships {
		switch mode {
		case "all":
			if relationship.Kind == "foreign_key" || relationship.Kind == "candidate_foreign_key" {
				included = append(included, relationship)
			}
		case "metadata":
			if relationship.Kind == "foreign_key" {
				included = append(included, relationship)
			}
		}
	}
	return included
}

func skippedCandidateCount(relationships []model.RelationshipPlan, mode string) int {
	if mode != "metadata" {
		return 0
	}
	var count int
	for _, relationship := range relationships {
		if relationship.Kind == "candidate_foreign_key" {
			count++
		}
	}
	return count
}

func FormatIssues(result Result) []string {
	lines := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		parts := []string{
			"plan_check",
			string(issue.Severity),
			"code=" + issue.Code,
		}
		if issue.Table != "" {
			parts = append(parts, "table="+issue.Table)
		}
		if issue.Field != "" {
			parts = append(parts, "field="+issue.Field)
		}
		if issue.Relationship != "" {
			parts = append(parts, "relationship="+issue.Relationship)
		}
		if issue.Detail != "" {
			parts = append(parts, "detail="+quote(issue.Detail))
		}
		lines = append(lines, strings.Join(parts, " "))
	}
	sort.Strings(lines)
	return lines
}

func quote(value string) string {
	return fmt.Sprintf("%q", value)
}
