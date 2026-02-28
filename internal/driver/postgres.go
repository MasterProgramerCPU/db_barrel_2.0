package driver

import (
	"database/sql"
	"fmt"

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
		schema.Tables = append(schema.Tables, Table{
			Name:        tableName,
			Columns:     cols,
			ForeignKeys: fks,
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
