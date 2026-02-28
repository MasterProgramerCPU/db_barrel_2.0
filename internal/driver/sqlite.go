package driver

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

func init() {
	Register("sqlite", func() Driver { return &SQLiteDriver{} })
}

// SQLiteDriver implements Driver for SQLite databases.
type SQLiteDriver struct {
	db *sql.DB
}

func (d *SQLiteDriver) Connect(dsn string) error {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("sqlite connect: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("sqlite ping: %w", err)
	}
	d.db = db
	return nil
}

func (d *SQLiteDriver) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

func (d *SQLiteDriver) Introspect() (*Schema, error) {
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

func (d *SQLiteDriver) getTables() ([]string, error) {
	rows, err := d.db.Query(`
		SELECT name FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY name
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

func (d *SQLiteDriver) getColumns(table string) ([]Column, error) {
	// PRAGMA table_info returns: cid, name, type, notnull, dflt_value, pk
	rows, err := d.db.Query(fmt.Sprintf("PRAGMA table_info('%s')", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var cid int
		var c Column
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &c.Name, &c.DataType, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		c.IsNullable = notnull == 0
		c.IsPrimaryKey = pk > 0
		if dflt.Valid {
			c.DefaultValue = dflt.String
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func (d *SQLiteDriver) getForeignKeys(table string) ([]ForeignKey, error) {
	// PRAGMA foreign_key_list returns: id, seq, table, from, to, on_update, on_delete, match
	rows, err := d.db.Query(fmt.Sprintf("PRAGMA foreign_key_list('%s')", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []ForeignKey
	for rows.Next() {
		var id, seq int
		var fk ForeignKey
		var onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &fk.ReferencedTable, &fk.ColumnName, &fk.ReferencedColumn, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		fk.ConstraintName = fmt.Sprintf("fk_%s_%d", table, id)
		fks = append(fks, fk)
	}
	return fks, rows.Err()
}
