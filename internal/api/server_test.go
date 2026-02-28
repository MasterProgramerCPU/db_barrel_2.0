package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/robotelu/db_barrel_2.0/internal/driver"
)

func testServer() *Server {
	// Create test schema
	schema := &driver.Schema{
		Tables: []driver.Table{
			{
				Name: "users",
				Columns: []driver.Column{
					{Name: "id", DataType: "INTEGER", IsPrimaryKey: true},
					{Name: "name", DataType: "TEXT"},
				},
			},
			{
				Name: "posts",
				Columns: []driver.Column{
					{Name: "id", DataType: "INTEGER", IsPrimaryKey: true},
					{Name: "user_id", DataType: "INTEGER"},
				},
				ForeignKeys: []driver.ForeignKey{
					{ConstraintName: "fk_posts_user", ColumnName: "user_id", ReferencedTable: "users", ReferencedColumn: "id"},
				},
			},
		},
	}

	databases := []DatabaseInfo{
		{ID: 0, Name: "Test DB", Driver: "sqlite", Status: "ok", TableCount: 2},
		{ID: 1, Name: "Broken DB", Driver: "postgresql", Status: "error", Error: "connection refused"},
	}

	schemas := map[int]*driver.Schema{0: schema}

	return NewServer(fstest.MapFS{}, databases, schemas)
}

func TestHandleDatabases(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("GET", "/api/databases", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var dbs []DatabaseInfo
	if err := json.NewDecoder(rec.Body).Decode(&dbs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dbs) != 2 {
		t.Fatalf("expected 2 databases, got %d", len(dbs))
	}
	if dbs[0].Name != "Test DB" {
		t.Errorf("expected name 'Test DB', got %q", dbs[0].Name)
	}
	if dbs[0].Driver != "sqlite" {
		t.Errorf("expected driver 'sqlite', got %q", dbs[0].Driver)
	}
	if dbs[1].Status != "error" {
		t.Errorf("expected status 'error', got %q", dbs[1].Status)
	}
}

func TestHandleSchemaOK(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("GET", "/api/databases/0/schema", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var schema driver.Schema
	if err := json.NewDecoder(rec.Body).Decode(&schema); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(schema.Tables) != 2 {
		t.Errorf("expected 2 tables, got %d", len(schema.Tables))
	}
}

func TestHandleSchemaBrokenDB(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("GET", "/api/databases/1/schema", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestHandleSchemaNotFound(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("GET", "/api/databases/99/schema", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
