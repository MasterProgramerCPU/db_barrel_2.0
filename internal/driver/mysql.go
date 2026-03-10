package driver

import (
	"database/sql"
	"fmt"
	"strings"

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
	dsn    string
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
	d.dsn = dsn

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

func (d *MySQLDriver) ListDatabases() ([]string, error) {
	rows, err := d.db.Query(`
		SELECT SCHEMA_NAME
		FROM information_schema.schemata
		WHERE SCHEMA_NAME NOT IN ('information_schema', 'performance_schema', 'mysql', 'sys')
		ORDER BY SCHEMA_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()

	var dbs []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		dbs = append(dbs, name)
	}
	return dbs, rows.Err()
}

func (d *MySQLDriver) IntrospectAll() (*MultiSchema, error) {
	dbNames, err := d.ListDatabases()
	if err != nil {
		return nil, err
	}

	multi := &MultiSchema{Databases: make([]DatabaseSchema, 0, len(dbNames))}
	origDB := d.dbName
	for _, dbName := range dbNames {
		// Switch database context.
		d.dbName = dbName
		schema, err := d.Introspect()
		if err != nil {
			continue
		}
		if len(schema.Tables) == 0 {
			continue
		}
		for i := range schema.Tables {
			schema.Tables[i].Database = dbName
		}
		multi.Databases = append(multi.Databases, DatabaseSchema{
			Name:   dbName,
			Tables: schema.Tables,
		})
	}
	d.dbName = origDB
	return multi, nil
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
		idxs, err := d.getIndexes(tableName)
		if err != nil {
			return nil, fmt.Errorf("indexes for %s: %w", tableName, err)
		}
		checks, _ := d.getCheckConstraints(tableName) // gracefully ignore on older MySQL
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

func (d *MySQLDriver) getIndexes(table string) ([]Index, error) {
	rows, err := d.db.Query(`
		SELECT
			INDEX_NAME,
			GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) AS columns,
			CASE WHEN NON_UNIQUE = 0 THEN 1 ELSE 0 END AS is_unique
		FROM information_schema.statistics
		WHERE table_schema = ?
		  AND table_name = ?
		  AND INDEX_NAME != 'PRIMARY'
		GROUP BY INDEX_NAME, NON_UNIQUE
		ORDER BY INDEX_NAME
	`, d.dbName, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []Index
	for rows.Next() {
		var idx Index
		var colsStr string
		var isUnique int
		if err := rows.Scan(&idx.Name, &colsStr, &isUnique); err != nil {
			return nil, err
		}
		idx.IsUnique = isUnique == 1
		if colsStr != "" {
			idx.Columns = splitCSV(colsStr)
		}
		indexes = append(indexes, idx)
	}
	return indexes, rows.Err()
}

func (d *MySQLDriver) getCheckConstraints(table string) ([]CheckConstraint, error) {
	// Only available in MySQL 8.0.16+ and MariaDB 10.2+
	rows, err := d.db.Query(`
		SELECT
			cc.CONSTRAINT_NAME,
			cc.CHECK_CLAUSE
		FROM information_schema.check_constraints cc
		JOIN information_schema.table_constraints tc
		  ON cc.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
		 AND cc.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA
		WHERE tc.TABLE_SCHEMA = ?
		  AND tc.TABLE_NAME = ?
		  AND tc.CONSTRAINT_TYPE = 'CHECK'
		ORDER BY cc.CONSTRAINT_NAME
	`, d.dbName, table)
	if err != nil {
		return nil, nil // gracefully return empty on older versions
	}
	defer rows.Close()

	var checks []CheckConstraint
	for rows.Next() {
		var c CheckConstraint
		if err := rows.Scan(&c.Name, &c.Expression); err != nil {
			return nil, nil
		}
		checks = append(checks, c)
	}
	return checks, rows.Err()
}

func splitCSV(s string) []string {
	parts := make([]string, 0)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}
