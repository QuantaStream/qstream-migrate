package model

import "time"

type SourceInfo struct {
	Kind      string `json:"kind" yaml:"kind"`
	Schema    string `json:"schema" yaml:"schema"`
	DSNMasked string `json:"dsn_masked,omitempty" yaml:"dsn_masked,omitempty"`
}

type Inventory struct {
	Version     int              `json:"version" yaml:"version"`
	GeneratedAt time.Time        `json:"generated_at" yaml:"generated_at"`
	Source      SourceInfo       `json:"source" yaml:"source"`
	Tables      []TableInventory `json:"tables" yaml:"tables"`
}

type TableInventory struct {
	Name        string            `json:"name" yaml:"name"`
	TableType   string            `json:"table_type,omitempty" yaml:"table_type,omitempty"`
	Engine      string            `json:"engine,omitempty" yaml:"engine,omitempty"`
	RowEstimate *int64            `json:"row_estimate,omitempty" yaml:"row_estimate,omitempty"`
	Comment     string            `json:"comment,omitempty" yaml:"comment,omitempty"`
	PrimaryKey  []string          `json:"primary_key,omitempty" yaml:"primary_key,omitempty"`
	Columns     []ColumnInventory `json:"columns" yaml:"columns"`
	Indexes     []IndexInventory  `json:"indexes,omitempty" yaml:"indexes,omitempty"`
	ForeignKeys []ForeignKey      `json:"foreign_keys,omitempty" yaml:"foreign_keys,omitempty"`
}

type ColumnInventory struct {
	Name                   string         `json:"name" yaml:"name"`
	Ordinal                int            `json:"ordinal" yaml:"ordinal"`
	Nullable               bool           `json:"nullable" yaml:"nullable"`
	DataType               string         `json:"data_type" yaml:"data_type"`
	ColumnType             string         `json:"column_type" yaml:"column_type"`
	CharacterMaximumLength *int64         `json:"character_maximum_length,omitempty" yaml:"character_maximum_length,omitempty"`
	NumericPrecision       *int64         `json:"numeric_precision,omitempty" yaml:"numeric_precision,omitempty"`
	NumericScale           *int64         `json:"numeric_scale,omitempty" yaml:"numeric_scale,omitempty"`
	DateTimePrecision      *int64         `json:"datetime_precision,omitempty" yaml:"datetime_precision,omitempty"`
	Default                *string        `json:"default,omitempty" yaml:"default,omitempty"`
	ColumnKey              string         `json:"column_key,omitempty" yaml:"column_key,omitempty"`
	Extra                  string         `json:"extra,omitempty" yaml:"extra,omitempty"`
	Comment                string         `json:"comment,omitempty" yaml:"comment,omitempty"`
	Collation              *string        `json:"collation,omitempty" yaml:"collation,omitempty"`
	Profile                *ColumnProfile `json:"profile,omitempty" yaml:"profile,omitempty"`
}

type ColumnProfile struct {
	RowCount        int64         `json:"row_count" yaml:"row_count"`
	NullCount       int64         `json:"null_count" yaml:"null_count"`
	NonNullCount    int64         `json:"non_null_count" yaml:"non_null_count"`
	DistinctCount   *int64        `json:"distinct_count,omitempty" yaml:"distinct_count,omitempty"`
	MinValue        *string       `json:"min_value,omitempty" yaml:"min_value,omitempty"`
	MaxValue        *string       `json:"max_value,omitempty" yaml:"max_value,omitempty"`
	MaxStringLength *int64        `json:"max_string_length,omitempty" yaml:"max_string_length,omitempty"`
	P95StringLength *int64        `json:"p95_string_length,omitempty" yaml:"p95_string_length,omitempty"`
	P99StringLength *int64        `json:"p99_string_length,omitempty" yaml:"p99_string_length,omitempty"`
	Samples         []ValueSample `json:"samples,omitempty" yaml:"samples,omitempty"`
	ProfileError    string        `json:"profile_error,omitempty" yaml:"profile_error,omitempty"`
}

type ValueSample struct {
	Value string `json:"value" yaml:"value"`
	Count int64  `json:"count" yaml:"count"`
}

type IndexInventory struct {
	Name      string        `json:"name" yaml:"name"`
	Unique    bool          `json:"unique" yaml:"unique"`
	Type      string        `json:"type,omitempty" yaml:"type,omitempty"`
	Columns   []IndexColumn `json:"columns" yaml:"columns"`
	Cardinals []int64       `json:"cardinalities,omitempty" yaml:"cardinalities,omitempty"`
}

type IndexColumn struct {
	Name     string `json:"name" yaml:"name"`
	Ordinal  int    `json:"ordinal" yaml:"ordinal"`
	Nullable bool   `json:"nullable,omitempty" yaml:"nullable,omitempty"`
}

type ForeignKey struct {
	Name          string   `json:"name" yaml:"name"`
	Columns       []string `json:"columns" yaml:"columns"`
	ParentTable   string   `json:"parent_table" yaml:"parent_table"`
	ParentColumns []string `json:"parent_columns" yaml:"parent_columns"`
}

type AnalyzerSettings struct {
	StringEnumMaxDistinct  int64 `json:"string_enum_max_distinct" yaml:"string_enum_max_distinct"`
	DefaultLexPrefixLength int   `json:"default_lex_prefix_length" yaml:"default_lex_prefix_length"`
}

type MigrationPlan struct {
	Version     int              `json:"version" yaml:"version"`
	GeneratedAt time.Time        `json:"generated_at" yaml:"generated_at"`
	Source      SourceInfo       `json:"source" yaml:"source"`
	Settings    AnalyzerSettings `json:"settings" yaml:"settings"`
	Tables      []TablePlan      `json:"tables" yaml:"tables"`
	Notes       []string         `json:"notes,omitempty" yaml:"notes,omitempty"`
}

type TablePlan struct {
	Name          string             `json:"name" yaml:"name"`
	SourceName    string             `json:"source_name" yaml:"source_name"`
	Include       bool               `json:"include" yaml:"include"`
	PrimaryKey    []string           `json:"primary_key,omitempty" yaml:"primary_key,omitempty"`
	RowCount      int64              `json:"row_count,omitempty" yaml:"row_count,omitempty"`
	TimeQuantum   *TimeQuantumPlan   `json:"time_quantum,omitempty" yaml:"time_quantum,omitempty"`
	Fields        []FieldPlan        `json:"fields" yaml:"fields"`
	Relationships []RelationshipPlan `json:"relationships,omitempty" yaml:"relationships,omitempty"`
	Notes         []string           `json:"notes,omitempty" yaml:"notes,omitempty"`
}

type TimeQuantumPlan struct {
	Field           string   `json:"field,omitempty" yaml:"field,omitempty"`
	Type            string   `json:"type,omitempty" yaml:"type,omitempty"`
	CandidateFields []string `json:"candidate_fields,omitempty" yaml:"candidate_fields,omitempty"`
	Rationale       []string `json:"rationale,omitempty" yaml:"rationale,omitempty"`
	Warnings        []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

type FieldPlan struct {
	Name                       string   `json:"name" yaml:"name"`
	SourceName                 string   `json:"source_name" yaml:"source_name"`
	Include                    bool     `json:"include" yaml:"include"`
	SourceType                 string   `json:"source_type" yaml:"source_type"`
	Nullable                   bool     `json:"nullable" yaml:"nullable"`
	RecommendedMappingStrategy string   `json:"recommended_mapping_strategy" yaml:"recommended_mapping_strategy"`
	QuantaStreamType           string   `json:"quantastream_type" yaml:"quantastream_type"`
	MaxLen                     *int64   `json:"max_len,omitempty" yaml:"max_len,omitempty"`
	Scale                      *int64   `json:"scale,omitempty" yaml:"scale,omitempty"`
	LexPrefixLength            *int     `json:"lex_prefix_length,omitempty" yaml:"lex_prefix_length,omitempty"`
	ColumnID                   bool     `json:"column_id,omitempty" yaml:"column_id,omitempty"`
	SourceOrdinal              int      `json:"source_ordinal" yaml:"source_ordinal"`
	Rationale                  []string `json:"rationale,omitempty" yaml:"rationale,omitempty"`
	Warnings                   []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

type RelationshipPlan struct {
	Name          string   `json:"name" yaml:"name"`
	Kind          string   `json:"kind" yaml:"kind"`
	Confidence    string   `json:"confidence" yaml:"confidence"`
	Columns       []string `json:"columns" yaml:"columns"`
	ParentTable   string   `json:"parent_table" yaml:"parent_table"`
	ParentColumns []string `json:"parent_columns" yaml:"parent_columns"`
	Warnings      []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}
