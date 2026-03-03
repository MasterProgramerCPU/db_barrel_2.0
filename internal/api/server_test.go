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
				Indexes: []driver.Index{
					{Name: "idx_users_name", Columns: []string{"name"}, IsUnique: false},
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
				Indexes: []driver.Index{
					{Name: "idx_posts_user_id", Columns: []string{"user_id"}, IsUnique: false},
				},
			},
		},
	}

	databases := []DatabaseInfo{
		{ID: 0, Name: "Test DB", Driver: "sqlite", Status: "ok", TableCount: 2, Host: "localhost"},
		{ID: 1, Name: "Broken DB", Driver: "postgresql", Status: "error", Error: "connection refused", Host: "db.example.com", Port: 5432},
	}

	schemas := map[int]*driver.Schema{0: schema}
	replication := []ReplicationInfo{
		{SourceName: "Test DB", TargetName: "Broken DB", Type: "streaming"},
	}

	reloadFunc := func() ([]DatabaseInfo, map[int]*driver.Schema, []ReplicationInfo) {
		return databases, schemas, replication
	}

	return NewServer(fstest.MapFS{}, databases, schemas, replication, reloadFunc)
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
	if dbs[1].Host != "db.example.com" {
		t.Errorf("expected host 'db.example.com', got %q", dbs[1].Host)
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
	// Verify indexes are present
	if len(schema.Tables[0].Indexes) != 1 {
		t.Errorf("expected 1 index on users table, got %d", len(schema.Tables[0].Indexes))
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

func TestHandleReload(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("POST", "/api/reload", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", result["status"])
	}
}

func TestHandleTopology(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("GET", "/api/topology", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var repl []ReplicationInfo
	if err := json.NewDecoder(rec.Body).Decode(&repl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(repl) != 1 {
		t.Fatalf("expected 1 replication link, got %d", len(repl))
	}
	if repl[0].SourceName != "Test DB" {
		t.Errorf("expected source 'Test DB', got %q", repl[0].SourceName)
	}
}
