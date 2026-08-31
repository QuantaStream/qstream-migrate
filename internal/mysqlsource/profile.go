package mysqlsource

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

type ProfileOptions struct {
	Enabled      bool
	SampleLimit  int
	QueryTimeout time.Duration
}

func AddProfiles(ctx context.Context, db *sql.DB, inv *model.Inventory, opts ProfileOptions) {
	if !opts.Enabled {
		return
	}
	if opts.SampleLimit < 0 {
		opts.SampleLimit = 0
	}
	if opts.QueryTimeout <= 0 {
		opts.QueryTimeout = 30 * time.Second
	}

	for tableIdx := range inv.Tables {
		table := &inv.Tables[tableIdx]
		for colIdx := range table.Columns {
			column := &table.Columns[colIdx]
			profile, err := profileColumn(ctx, db, inv.Source.Schema, table.Name, *column, opts)
			if err != nil {
				profile = &model.ColumnProfile{ProfileError: err.Error()}
			}
			column.Profile = profile
		}
	}
}

func profileColumn(ctx context.Context, db *sql.DB, schema, table string, column model.ColumnInventory, opts ProfileOptions) (*model.ColumnProfile, error) {
	col := quoteIdent(column.Name)
	tbl := qualifiedTable(schema, table)

	queryCtx, cancel := context.WithTimeout(ctx, opts.QueryTimeout)
	defer cancel()

	query := fmt.Sprintf(`
SELECT COUNT(*) AS row_count,
       COALESCE(SUM(CASE WHEN %[1]s IS NULL THEN 1 ELSE 0 END), 0) AS null_count,
       COUNT(DISTINCT %[1]s) AS distinct_count,
       MIN(%[1]s) AS min_value,
       MAX(%[1]s) AS max_value
FROM %[2]s`, col, tbl)

	var rowCount, nullCount, distinctCount int64
	var minValue, maxValue any
	if err := db.QueryRowContext(queryCtx, query).Scan(&rowCount, &nullCount, &distinctCount, &minValue, &maxValue); err != nil {
		return nil, fmt.Errorf("profile %s.%s: %w", table, column.Name, err)
	}

	profile := &model.ColumnProfile{
		RowCount:      rowCount,
		NullCount:     nullCount,
		NonNullCount:  rowCount - nullCount,
		DistinctCount: &distinctCount,
	}
	if value := stringPtr(minValue); value != nil {
		profile.MinValue = value
	}
	if value := stringPtr(maxValue); value != nil {
		profile.MaxValue = value
	}

	if isStringColumn(column.DataType) {
		if err := addStringLengthStats(queryCtx, db, tbl, col, profile); err != nil {
			return nil, err
		}
	}
	if opts.SampleLimit > 0 {
		samples, err := sampleValues(queryCtx, db, tbl, col, opts.SampleLimit)
		if err != nil {
			return nil, err
		}
		profile.Samples = samples
	}
	return profile, nil
}

func addStringLengthStats(ctx context.Context, db *sql.DB, tableExpr, columnExpr string, profile *model.ColumnProfile) error {
	query := fmt.Sprintf(`
SELECT CHAR_LENGTH(%[1]s) AS value_length, COUNT(*) AS value_count
FROM %[2]s
WHERE %[1]s IS NOT NULL
GROUP BY CHAR_LENGTH(%[1]s)
ORDER BY value_length`, columnExpr, tableExpr)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("profile string lengths: %w", err)
	}
	defer rows.Close()

	var buckets []lengthBucket
	var total int64
	for rows.Next() {
		var length, count int64
		if err := rows.Scan(&length, &count); err != nil {
			return fmt.Errorf("scan string length bucket: %w", err)
		}
		buckets = append(buckets, lengthBucket{length: length, count: count})
		total += count
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate string length buckets: %w", err)
	}
	if len(buckets) == 0 {
		return nil
	}

	maxLen := buckets[len(buckets)-1].length
	p95 := percentileLength(buckets, total, 0.95)
	p99 := percentileLength(buckets, total, 0.99)
	profile.MaxStringLength = &maxLen
	profile.P95StringLength = &p95
	profile.P99StringLength = &p99
	return nil
}

type lengthBucket struct {
	length int64
	count  int64
}

func percentileLength(buckets []lengthBucket, total int64, percentile float64) int64 {
	if total <= 0 {
		return 0
	}
	target := int64(float64(total) * percentile)
	if target < 1 {
		target = 1
	}
	var seen int64
	for _, bucket := range buckets {
		seen += bucket.count
		if seen >= target {
			return bucket.length
		}
	}
	return buckets[len(buckets)-1].length
}

func sampleValues(ctx context.Context, db *sql.DB, tableExpr, columnExpr string, limit int) ([]model.ValueSample, error) {
	query := fmt.Sprintf(`
SELECT CAST(%[1]s AS CHAR) AS sample_value, COUNT(*) AS value_count
FROM %[2]s
WHERE %[1]s IS NOT NULL
GROUP BY %[1]s
ORDER BY value_count DESC, sample_value
LIMIT ?`, columnExpr, tableExpr)

	rows, err := db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("sample values: %w", err)
	}
	defer rows.Close()

	var samples []model.ValueSample
	for rows.Next() {
		var value sql.NullString
		var count int64
		if err := rows.Scan(&value, &count); err != nil {
			return nil, fmt.Errorf("scan sample value: %w", err)
		}
		if value.Valid {
			samples = append(samples, model.ValueSample{Value: value.String, Count: count})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sample values: %w", err)
	}
	return samples, nil
}

func isStringColumn(dataType string) bool {
	switch strings.ToLower(dataType) {
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext", "enum", "set":
		return true
	default:
		return false
	}
}

func stringPtr(value any) *string {
	if value == nil {
		return nil
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case []byte:
		text = string(typed)
	case time.Time:
		text = typed.Format(time.RFC3339Nano)
	default:
		text = fmt.Sprint(typed)
	}
	return &text
}
