package mysqlsource

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

type InspectOptions struct {
	Schema string
	Tables []string
}

func Introspect(ctx context.Context, db *sql.DB, opts InspectOptions) (model.Inventory, error) {
	if strings.TrimSpace(opts.Schema) == "" {
		return model.Inventory{}, fmt.Errorf("schema is required")
	}

	filter := tableFilter(opts.Tables)
	inv := model.Inventory{
		Version:     1,
		GeneratedAt: time.Now().UTC(),
		Source: model.SourceInfo{
			Kind:   "mysql",
			Schema: opts.Schema,
		},
	}

	tables, err := readTables(ctx, db, opts.Schema, filter)
	if err != nil {
		return model.Inventory{}, err
	}
	if len(tables) == 0 {
		return model.Inventory{}, fmt.Errorf("no base tables found in schema %q", opts.Schema)
	}
	inv.Tables = tables

	if err := readColumns(ctx, db, opts.Schema, &inv, filter); err != nil {
		return model.Inventory{}, err
	}
	if err := readPrimaryKeys(ctx, db, opts.Schema, &inv, filter); err != nil {
		return model.Inventory{}, err
	}
	if err := readIndexes(ctx, db, opts.Schema, &inv, filter); err != nil {
		return model.Inventory{}, err
	}
	if err := readForeignKeys(ctx, db, opts.Schema, &inv, filter); err != nil {
		return model.Inventory{}, err
	}

	sort.Slice(inv.Tables, func(i, j int) bool {
		return inv.Tables[i].Name < inv.Tables[j].Name
	})
	return inv, nil
}

func tableFilter(tables []string) map[string]bool {
	if len(tables) == 0 {
		return nil
	}
	filter := make(map[string]bool, len(tables))
	for _, table := range tables {
		table = strings.TrimSpace(table)
		if table != "" {
			filter[table] = true
		}
	}
	if len(filter) == 0 {
		return nil
	}
	return filter
}

func includeTable(filter map[string]bool, table string) bool {
	return filter == nil || filter[table]
}

func tableIndex(inv *model.Inventory) map[string]int {
	index := make(map[string]int, len(inv.Tables))
	for i := range inv.Tables {
		index[inv.Tables[i].Name] = i
	}
	return index
}

func readTables(ctx context.Context, db *sql.DB, schema string, filter map[string]bool) ([]model.TableInventory, error) {
	rows, err := db.QueryContext(ctx, `
SELECT TABLE_NAME, TABLE_TYPE, ENGINE, TABLE_ROWS, TABLE_COMMENT
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'
ORDER BY TABLE_NAME`, schema)
	if err != nil {
		return nil, fmt.Errorf("read tables: %w", err)
	}
	defer rows.Close()

	var tables []model.TableInventory
	seen := map[string]bool{}
	for rows.Next() {
		var name, tableType string
		var engine, comment sql.NullString
		var rowsEstimate sql.NullInt64
		if err := rows.Scan(&name, &tableType, &engine, &rowsEstimate, &comment); err != nil {
			return nil, fmt.Errorf("scan table: %w", err)
		}
		seen[name] = true
		if !includeTable(filter, name) {
			continue
		}
		table := model.TableInventory{
			Name:      name,
			TableType: tableType,
			Engine:    engine.String,
			Comment:   comment.String,
		}
		if rowsEstimate.Valid {
			table.RowEstimate = &rowsEstimate.Int64
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tables: %w", err)
	}
	if filter != nil {
		var missing []string
		for table := range filter {
			if !seen[table] {
				missing = append(missing, table)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			return nil, fmt.Errorf("requested tables not found in schema %q: %s", schema, strings.Join(missing, ", "))
		}
	}
	return tables, nil
}

func readColumns(ctx context.Context, db *sql.DB, schema string, inv *model.Inventory, filter map[string]bool) error {
	rows, err := db.QueryContext(ctx, `
SELECT TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION, COLUMN_DEFAULT, IS_NULLABLE,
       DATA_TYPE, COLUMN_TYPE, CHARACTER_MAXIMUM_LENGTH, NUMERIC_PRECISION,
       NUMERIC_SCALE, DATETIME_PRECISION, COLUMN_KEY, EXTRA, COLUMN_COMMENT,
       COLLATION_NAME
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ?
ORDER BY TABLE_NAME, ORDINAL_POSITION`, schema)
	if err != nil {
		return fmt.Errorf("read columns: %w", err)
	}
	defer rows.Close()

	tables := tableIndex(inv)
	for rows.Next() {
		var table, name, nullable, dataType, columnType, columnKey, extra, comment string
		var ordinal int
		var defaultValue, collation sql.NullString
		var charMax, precision, scale, datetimePrecision sql.NullInt64
		if err := rows.Scan(
			&table,
			&name,
			&ordinal,
			&defaultValue,
			&nullable,
			&dataType,
			&columnType,
			&charMax,
			&precision,
			&scale,
			&datetimePrecision,
			&columnKey,
			&extra,
			&comment,
			&collation,
		); err != nil {
			return fmt.Errorf("scan column: %w", err)
		}
		if !includeTable(filter, table) {
			continue
		}
		i, ok := tables[table]
		if !ok {
			continue
		}
		column := model.ColumnInventory{
			Name:       name,
			Ordinal:    ordinal,
			Nullable:   nullable == "YES",
			DataType:   dataType,
			ColumnType: columnType,
			ColumnKey:  columnKey,
			Extra:      extra,
			Comment:    comment,
		}
		if defaultValue.Valid {
			column.Default = &defaultValue.String
		}
		if charMax.Valid {
			column.CharacterMaximumLength = &charMax.Int64
		}
		if precision.Valid {
			column.NumericPrecision = &precision.Int64
		}
		if scale.Valid {
			column.NumericScale = &scale.Int64
		}
		if datetimePrecision.Valid {
			column.DateTimePrecision = &datetimePrecision.Int64
		}
		if collation.Valid {
			column.Collation = &collation.String
		}
		inv.Tables[i].Columns = append(inv.Tables[i].Columns, column)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate columns: %w", err)
	}
	return nil
}

func readPrimaryKeys(ctx context.Context, db *sql.DB, schema string, inv *model.Inventory, filter map[string]bool) error {
	rows, err := db.QueryContext(ctx, `
SELECT TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION
FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = ? AND CONSTRAINT_NAME = 'PRIMARY'
ORDER BY TABLE_NAME, ORDINAL_POSITION`, schema)
	if err != nil {
		return fmt.Errorf("read primary keys: %w", err)
	}
	defer rows.Close()

	tables := tableIndex(inv)
	for rows.Next() {
		var table, column string
		var ordinal int
		if err := rows.Scan(&table, &column, &ordinal); err != nil {
			return fmt.Errorf("scan primary key: %w", err)
		}
		if !includeTable(filter, table) {
			continue
		}
		if i, ok := tables[table]; ok {
			inv.Tables[i].PrimaryKey = append(inv.Tables[i].PrimaryKey, column)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate primary keys: %w", err)
	}
	return nil
}

func readIndexes(ctx context.Context, db *sql.DB, schema string, inv *model.Inventory, filter map[string]bool) error {
	rows, err := db.QueryContext(ctx, `
SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME,
       INDEX_TYPE, NULLABLE, CARDINALITY
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ?
ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`, schema)
	if err != nil {
		return fmt.Errorf("read indexes: %w", err)
	}
	defer rows.Close()

	tables := tableIndex(inv)
	indexes := make(map[string]int)
	for rows.Next() {
		var table, name, column, indexType string
		var nullable sql.NullString
		var nonUnique, ordinal int
		var cardinality sql.NullInt64
		if err := rows.Scan(&table, &name, &nonUnique, &ordinal, &column, &indexType, &nullable, &cardinality); err != nil {
			return fmt.Errorf("scan index: %w", err)
		}
		if !includeTable(filter, table) {
			continue
		}
		tablePos, ok := tables[table]
		if !ok {
			continue
		}
		key := table + "\x00" + name
		indexPos, ok := indexes[key]
		if !ok {
			inv.Tables[tablePos].Indexes = append(inv.Tables[tablePos].Indexes, model.IndexInventory{
				Name:   name,
				Unique: nonUnique == 0,
				Type:   indexType,
			})
			indexPos = len(inv.Tables[tablePos].Indexes) - 1
			indexes[key] = indexPos
		}
		idx := &inv.Tables[tablePos].Indexes[indexPos]
		idx.Columns = append(idx.Columns, model.IndexColumn{
			Name:     column,
			Ordinal:  ordinal,
			Nullable: nullable.Valid && nullable.String == "YES",
		})
		if cardinality.Valid {
			idx.Cardinals = append(idx.Cardinals, cardinality.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate indexes: %w", err)
	}
	return nil
}

func readForeignKeys(ctx context.Context, db *sql.DB, schema string, inv *model.Inventory, filter map[string]bool) error {
	rows, err := db.QueryContext(ctx, `
SELECT CONSTRAINT_NAME, TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION,
       REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = ? AND REFERENCED_TABLE_NAME IS NOT NULL
ORDER BY TABLE_NAME, CONSTRAINT_NAME, ORDINAL_POSITION`, schema)
	if err != nil {
		return fmt.Errorf("read foreign keys: %w", err)
	}
	defer rows.Close()

	tables := tableIndex(inv)
	foreignKeys := make(map[string]int)
	for rows.Next() {
		var name, table, column, parentTable, parentColumn string
		var ordinal int
		if err := rows.Scan(&name, &table, &column, &ordinal, &parentTable, &parentColumn); err != nil {
			return fmt.Errorf("scan foreign key: %w", err)
		}
		if !includeTable(filter, table) {
			continue
		}
		tablePos, ok := tables[table]
		if !ok {
			continue
		}
		key := table + "\x00" + name
		fkPos, ok := foreignKeys[key]
		if !ok {
			inv.Tables[tablePos].ForeignKeys = append(inv.Tables[tablePos].ForeignKeys, model.ForeignKey{
				Name:        name,
				ParentTable: parentTable,
			})
			fkPos = len(inv.Tables[tablePos].ForeignKeys) - 1
			foreignKeys[key] = fkPos
		}
		fk := &inv.Tables[tablePos].ForeignKeys[fkPos]
		fk.Columns = append(fk.Columns, column)
		fk.ParentColumns = append(fk.ParentColumns, parentColumn)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate foreign keys: %w", err)
	}
	return nil
}
