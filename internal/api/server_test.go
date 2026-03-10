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
	schema := &driver.MultiSchema{
		Databases: []driver.DatabaseSchema{
			{
				Name: "main",
				Tables: []driver.Table{
					{
						Name:     "users",
						Database: "main",
						Columns: []driver.Column{
							{Name: "id", DataType: "INTEGER", IsPrimaryKey: true},
							{Name: "name", DataType: "TEXT"},
						},
						Indexes: []driver.Index{
							{Name: "idx_users_name", Columns: []string{"name"}, IsUnique: false},
						},
					},
					{
						Name:     "posts",
						Database: "main",
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
			},
		},
	}

	databases := []DatabaseInfo{
		{ID: 0, Name: "Test DB", Driver: "sqlite", Status: "ok", TableCount: 2, Host: "localhost"},
		{ID: 1, Name: "Broken DB", Driver: "postgresql", Status: "error", Error: "connection refused", Host: "db.example.com", Port: 5432},
	}

	schemas := map[int]*driver.MultiSchema{0: schema}
	replication := []ReplicationInfo{
		{SourceName: "Test DB", TargetName: "Broken DB", Type: "streaming"},
	}
	replReport := ReplicationReport{
		GeneratedAt: "2026-03-09T00:00:00Z",
		Summary: ReplicationSummary{
			ConfiguredDatabases:         2,
			ConfiguredPostgresDatabases: 1,
			AutoDiscoveredLinks:         0,
			MergedLinks:                 1,
			DroppedLinks:                0,
			EndpointErrors:              0,
		},
		FinalLinks: replication,
	}

	reloadFunc := func() ([]DatabaseInfo, map[int]*driver.MultiSchema, []ReplicationInfo, ReplicationReport) {
		return databases, schemas, replication, replReport
	}

	return NewServer(fstest.MapFS{}, databases, schemas, replication, replReport, reloadFunc)
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
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected wildcard CORS header, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
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

	var multi driver.MultiSchema
	if err := json.NewDecoder(rec.Body).Decode(&multi); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(multi.Databases) != 1 {
		t.Errorf("expected 1 database group, got %d", len(multi.Databases))
	}
	if len(multi.Databases[0].Tables) != 2 {
		t.Errorf("expected 2 tables, got %d", len(multi.Databases[0].Tables))
	}
	// Verify indexes are present
	if len(multi.Databases[0].Tables[0].Indexes) != 1 {
		t.Errorf("expected 1 index on users table, got %d", len(multi.Databases[0].Tables[0].Indexes))
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

func TestHandleTopologyReport(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("GET", "/api/topology/report", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var report ReplicationReport
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Summary.MergedLinks != 1 {
		t.Fatalf("expected mergedLinks=1, got %d", report.Summary.MergedLinks)
	}
}

func TestCORSReflectsOriginAndHandlesPreflight(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("OPTIONS", "/api/databases", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, X-Custom")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Fatalf("expected reflected origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Headers") != "Content-Type, X-Custom" {
		t.Fatalf("expected reflected request headers, got %q", rec.Header().Get("Access-Control-Allow-Headers"))
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty preflight body, got %q", rec.Body.String())
	}
}
