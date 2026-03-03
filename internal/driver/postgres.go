package driver

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
)

func init() {
	Register("postgresql", func() Driver { return &PostgresDriver{} })
}

// PostgresDriver implements Driver for PostgreSQL databases.
type PostgresDriver struct {
	db *sql.DB
}

func (d *PostgresDriver) Connect(dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("postgres connect: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("postgres ping: %w", err)
	}
	d.db = db
	return nil
}

func (d *PostgresDriver) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

func (d *PostgresDriver) Introspect() (*Schema, error) {
	tables, err := d.getTables()
	if err != nil {
		return nil, err
	}

	schema := &Schema{Tables: make([]Table, 0, len(tables))}
	for _, tableName := range tables {
		cols, err := d.getColumns(tableName)
		if err != nil {
			return nil, fmt.Errorf("columns for %s: %w", tableName, err)
		}
		pks, err := d.getPrimaryKeys(tableName)
		if err != nil {
			return nil, fmt.Errorf("primary keys for %s: %w", tableName, err)
		}
		for i := range cols {
			if pks[cols[i].Name] {
				cols[i].IsPrimaryKey = true
			}
		}
		fks, err := d.getForeignKeys(tableName)
		if err != nil {
			return nil, fmt.Errorf("foreign keys for %s: %w", tableName, err)
		}
		idxs, err := d.getIndexes(tableName)
		if err != nil {
			return nil, fmt.Errorf("indexes for %s: %w", tableName, err)
		}
		checks, err := d.getCheckConstraints(tableName)
		if err != nil {
			return nil, fmt.Errorf("check constraints for %s: %w", tableName, err)
		}
		schema.Tables = append(schema.Tables, Table{
			Name:             tableName,
			Columns:          cols,
			ForeignKeys:      fks,
			Indexes:          idxs,
			CheckConstraints: checks,
		})
	}
	return schema, nil
}

func (d *PostgresDriver) getTables() ([]string, error) {
	rows, err := d.db.Query(`
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'public'
		  AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func (d *PostgresDriver) getColumns(table string) ([]Column, error) {
	rows, err := d.db.Query(`
		SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = $1
		ORDER BY ordinal_position
	`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var c Column
		var nullable string
		if err := rows.Scan(&c.Name, &c.DataType, &nullable, &c.DefaultValue); err != nil {
			return nil, err
		}
		c.IsNullable = nullable == "YES"
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func (d *PostgresDriver) getPrimaryKeys(table string) (map[string]bool, error) {
	rows, err := d.db.Query(`
		SELECT kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name
		 AND tc.table_schema = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema = 'public'
		  AND tc.table_name = $1
	`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pks := make(map[string]bool)
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		pks[col] = true
	}
	return pks, rows.Err()
}

func (d *PostgresDriver) getForeignKeys(table string) ([]ForeignKey, error) {
	rows, err := d.db.Query(`
		SELECT
			tc.constraint_name,
			kcu.column_name,
			ccu.table_name AS referenced_table,
			ccu.column_name AS referenced_column
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name
		 AND tc.table_schema = kcu.table_schema
		JOIN information_schema.constraint_column_usage ccu
		  ON ccu.constraint_name = tc.constraint_name
		 AND ccu.table_schema = tc.table_schema
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema = 'public'
		  AND tc.table_name = $1
	`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []ForeignKey
	for rows.Next() {
		var fk ForeignKey
		if err := rows.Scan(&fk.ConstraintName, &fk.ColumnName, &fk.ReferencedTable, &fk.ReferencedColumn); err != nil {
			return nil, err
		}
		fks = append(fks, fk)
	}
	return fks, rows.Err()
}

func (d *PostgresDriver) getIndexes(table string) ([]Index, error) {
	rows, err := d.db.Query(`
		SELECT
			i.relname AS index_name,
			ix.indisunique AS is_unique,
			array_agg(a.attname ORDER BY array_position(ix.indkey, a.attnum)) AS columns
		FROM pg_class t
		JOIN pg_index ix ON t.oid = ix.indrelid
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(ix.indkey)
		WHERE t.relname = $1
		  AND n.nspname = 'public'
		  AND NOT ix.indisprimary
		GROUP BY i.relname, ix.indisunique
		ORDER BY i.relname
	`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []Index
	for rows.Next() {
		var idx Index
		var colsStr string
		if err := rows.Scan(&idx.Name, &idx.IsUnique, &colsStr); err != nil {
			return nil, err
		}
		// Parse PostgreSQL array format: {col1,col2}
		colsStr = strings.Trim(colsStr, "{}")
		if colsStr != "" {
			idx.Columns = strings.Split(colsStr, ",")
		}
		indexes = append(indexes, idx)
	}
	return indexes, rows.Err()
}

func (d *PostgresDriver) getCheckConstraints(table string) ([]CheckConstraint, error) {
	rows, err := d.db.Query(`
		SELECT
			con.conname AS constraint_name,
			pg_get_constraintdef(con.oid) AS expression
		FROM pg_constraint con
		JOIN pg_class rel ON rel.oid = con.conrelid
		JOIN pg_namespace nsp ON nsp.oid = rel.relnamespace
		WHERE con.contype = 'c'
		  AND nsp.nspname = 'public'
		  AND rel.relname = $1
		ORDER BY con.conname
	`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var checks []CheckConstraint
	for rows.Next() {
		var c CheckConstraint
		if err := rows.Scan(&c.Name, &c.Expression); err != nil {
			return nil, err
		}
		checks = append(checks, c)
	}
	return checks, rows.Err()
}
