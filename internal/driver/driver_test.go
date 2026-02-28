package driver

import (
	"testing"
)

func TestRegistryList(t *testing.T) {
	drivers := List()
	if len(drivers) == 0 {
		t.Fatal("expected at least one registered driver")
	}

	expected := map[string]bool{
		"postgresql": false,
		"mysql":      false,
		"mariadb":    false,
		"sqlite":     false,
	}

	for _, name := range drivers {
		if _, ok := expected[name]; ok {
			expected[name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("expected driver %q to be registered", name)
		}
	}
}

func TestNewDriverValid(t *testing.T) {
	for _, name := range []string{"postgresql", "mysql", "mariadb", "sqlite"} {
		drv, err := New(name)
		if err != nil {
			t.Errorf("New(%q): unexpected error: %v", name, err)
		}
		if drv == nil {
			t.Errorf("New(%q): returned nil driver", name)
		}
	}
}

func TestNewDriverUnknown(t *testing.T) {
	_, err := New("oracle")
	if err == nil {
		t.Fatal("expected error for unknown driver, got nil")
	}
}

func TestSQLiteIntrospect(t *testing.T) {
	drv, err := New("sqlite")
	if err != nil {
		t.Fatalf("failed to create sqlite driver: %v", err)
	}

	// Connect to the test database
	err = drv.Connect("../../testdata/test_barrel.db")
	if err != nil {
		t.Skipf("skipping SQLite introspection test: %v (create testdata/test_barrel.db first)", err)
	}
	defer drv.Close()

	schema, err := drv.Introspect()
	if err != nil {
		t.Fatalf("introspect failed: %v", err)
	}

	if len(schema.Tables) != 5 {
		t.Errorf("expected 5 tables, got %d", len(schema.Tables))
	}

	// Check that we find the users table with expected columns
	var usersTable *Table
	for i := range schema.Tables {
		if schema.Tables[i].Name == "users" {
			usersTable = &schema.Tables[i]
			break
		}
	}
	if usersTable == nil {
		t.Fatal("expected to find 'users' table")
	}
	if len(usersTable.Columns) != 4 {
		t.Errorf("expected 4 columns in users table, got %d", len(usersTable.Columns))
	}

	// Check that the comments table has 2 foreign keys
	var commentsTable *Table
	for i := range schema.Tables {
		if schema.Tables[i].Name == "comments" {
			commentsTable = &schema.Tables[i]
			break
		}
	}
	if commentsTable == nil {
		t.Fatal("expected to find 'comments' table")
	}
	if len(commentsTable.ForeignKeys) != 2 {
		t.Errorf("expected 2 foreign keys in comments table, got %d", len(commentsTable.ForeignKeys))
	}
}
