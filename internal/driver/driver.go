// Package driver provides a database driver abstraction for schema introspection.
package driver

import (
	"fmt"
	"sync"
)

// Column represents a single column in a database table.
type Column struct {
	Name         string `json:"name"`
	DataType     string `json:"dataType"`
	IsNullable   bool   `json:"isNullable"`
	IsPrimaryKey bool   `json:"isPrimaryKey"`
	DefaultValue string `json:"defaultValue,omitempty"`
}

// ForeignKey represents a foreign key relationship.
type ForeignKey struct {
	ConstraintName   string `json:"constraintName"`
	ColumnName       string `json:"columnName"`
	ReferencedTable  string `json:"referencedTable"`
	ReferencedColumn string `json:"referencedColumn"`
}

// Table represents a database table with its columns and foreign keys.
type Table struct {
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	ForeignKeys []ForeignKey `json:"foreignKeys"`
}

// Schema represents the full database schema.
type Schema struct {
	Tables []Table `json:"tables"`
}

// Driver is the interface that database-specific drivers must implement.
type Driver interface {
	// Connect opens a connection to the database using the given DSN.
	Connect(dsn string) error
	// Close closes the database connection.
	Close() error
	// Introspect reads the database schema and returns it.
	Introspect() (*Schema, error)
}

// registry holds all registered driver constructors.
var (
	registryMu sync.RWMutex
	registry   = make(map[string]func() Driver)
)

// Register adds a driver constructor to the registry.
func Register(name string, constructor func() Driver) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = constructor
}

// New creates a new driver instance by name.
func New(name string) (Driver, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	constructor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown driver: %q", name)
	}
	return constructor(), nil
}

// List returns all registered driver names.
func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
