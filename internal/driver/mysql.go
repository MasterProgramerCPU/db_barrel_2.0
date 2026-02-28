package driver

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

func init() {
	constructor := func() Driver { return &MySQLDriver{} }
	Register("mysql", constructor)
	Register("mariadb", constructor)
}

// MySQLDriver implements Driver for MySQL and MariaDB databases.
type MySQLDriver struct {
	db     *sql.DB
	dbName string
}

func (d *MySQLDriver) Connect(dsn string) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("mysql connect: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("mysql ping: %w", err)
	}
	d.db = db

	// Determine the current database name for introspection queries.
	if err := db.QueryRow("SELECT DATABASE()").Scan(&d.dbName); err != nil {
		db.Close()
		return fmt.Errorf("mysql get database name: %w", err)
	}
	return nil
}

func (d *MySQLDriver) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

func (d *MySQLDriver) Introspect() (*Schema, error) {
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

func (d *MySQLDriver) getTables() ([]string, error) {
	rows, err := d.db.Query(`
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = ?
		  AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`, d.dbName)
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

func (d *MySQLDriver) getColumns(table string) ([]Column, error) {
	rows, err := d.db.Query(`
		SELECT column_name, column_type, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = ?
		  AND table_name = ?
		ORDER BY ordinal_position
	`, d.dbName, table)
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

func (d *MySQLDriver) getPrimaryKeys(table string) (map[string]bool, error) {
	rows, err := d.db.Query(`
		SELECT column_name
		FROM information_schema.key_column_usage
		WHERE table_schema = ?
		  AND table_name = ?
		  AND constraint_name = 'PRIMARY'
	`, d.dbName, table)
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

func (d *MySQLDriver) getForeignKeys(table string) ([]ForeignKey, error) {
	rows, err := d.db.Query(`
		SELECT
			constraint_name,
			column_name,
			referenced_table_name,
			referenced_column_name
		FROM information_schema.key_column_usage
		WHERE table_schema = ?
		  AND table_name = ?
		  AND referenced_table_name IS NOT NULL
	`, d.dbName, table)
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
