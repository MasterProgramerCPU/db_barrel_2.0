package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/robotelu/db_barrel_2.0/internal/config"
	"github.com/robotelu/db_barrel_2.0/internal/driver"
)

func testProjectState(projectName string) ProjectState {
	alphaSchema := &driver.MultiSchema{
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
					},
				},
			},
		},
	}

	betaSchema := &driver.MultiSchema{
		Databases: []driver.DatabaseSchema{
			{
				Name: "warehouse",
				Tables: []driver.Table{
					{
						Name:     "orders",
						Database: "warehouse",
						Columns: []driver.Column{
							{Name: "id", DataType: "INTEGER", IsPrimaryKey: true},
							{Name: "created_at", DataType: "TEXT"},
						},
					},
				},
			},
		},
	}

	projects := []ProjectInfo{
		{Name: "Alpha", DatabaseCount: 2},
		{Name: "Beta", DatabaseCount: 1},
	}

	if projectName == "Beta" {
		return ProjectState{
			Projects:       projects,
			CurrentProject: "Beta",
			Databases: []DatabaseInfo{
				{ID: 0, Name: "Warehouse", Driver: "sqlite", Status: "ok", TableCount: 1, Host: "localhost"},
			},
			Schemas: map[int]*driver.MultiSchema{0: betaSchema},
			Replication: []ReplicationInfo{
				{SourceName: "Warehouse", TargetName: "Warehouse Replica", Type: "streaming"},
			},
			ReplReport: ReplicationReport{
				GeneratedAt: "2026-03-13T00:00:00Z",
				Summary: ReplicationSummary{
					ConfiguredDatabases: 1,
					MergedLinks:         1,
				},
			},
		}
	}

	return ProjectState{
		Projects:       projects,
		CurrentProject: "Alpha",
		Databases: []DatabaseInfo{
			{ID: 0, Name: "App DB", Driver: "sqlite", Status: "ok", TableCount: 1, Host: "localhost"},
			{ID: 1, Name: "Broken DB", Driver: "postgresql", Status: "error", Error: "connection refused", Host: "db.example.com", Port: 5432},
		},
		Schemas: map[int]*driver.MultiSchema{0: alphaSchema},
		Replication: []ReplicationInfo{
			{SourceName: "App DB", TargetName: "Broken DB", Type: "streaming"},
		},
		ReplReport: ReplicationReport{
			GeneratedAt: "2026-03-13T00:00:00Z",
			Summary: ReplicationSummary{
				ConfiguredDatabases: 2,
				MergedLinks:         1,
			},
			FinalLinks: []ReplicationInfo{
				{SourceName: "App DB", TargetName: "Broken DB", Type: "streaming"},
			},
		},
	}
}

func testServer() *Server {
	projectStates := map[string]ProjectState{
		"Alpha": testProjectState("Alpha"),
		"Beta":  testProjectState("Beta"),
	}
	currentProject := "Alpha"

	syncProjects := func() {
		projectInfos := []ProjectInfo{
			{Name: "Alpha", DatabaseCount: len(projectStates["Alpha"].Databases)},
			{Name: "Beta", DatabaseCount: len(projectStates["Beta"].Databases)},
		}
		for name, state := range projectStates {
			state.Projects = projectInfos
			state.CurrentProject = name
			projectStates[name] = state
		}
	}
	syncProjects()

	reloadFunc := func(preferredProject string) (ProjectState, error) {
		if preferredProject != "" {
			if _, ok := projectStates[preferredProject]; ok {
				currentProject = preferredProject
			}
		}
		return projectStates[currentProject], nil
	}

	selectFunc := func(projectName string) (ProjectState, error) {
		state, ok := projectStates[projectName]
		if !ok {
			return ProjectState{}, fmt.Errorf("unknown project %q", projectName)
		}
		currentProject = projectName
		return state, nil
	}

	addFunc := func(projectName string, dbCfg config.DatabaseConfig) error {
		state, ok := projectStates[projectName]
		if !ok {
			return fmt.Errorf("unknown project %q", projectName)
		}
		id := len(state.Databases)
		state.Databases = append(state.Databases, DatabaseInfo{
			ID:         id,
			Name:       dbCfg.Name,
			Driver:     dbCfg.Driver,
			Status:     "ok",
			TableCount: 0,
			Host:       dbCfg.Host,
			Port:       dbCfg.Port,
		})
		if state.Schemas == nil {
			state.Schemas = make(map[int]*driver.MultiSchema)
		}
		state.Schemas[id] = &driver.MultiSchema{
			Databases: []driver.DatabaseSchema{{Name: dbCfg.Database}},
		}
		projectStates[projectName] = state
		syncProjects()
		return nil
	}

	deleteFunc := func(projectName string, index int) error {
		state, ok := projectStates[projectName]
		if !ok {
			return fmt.Errorf("unknown project %q", projectName)
		}
		if index < 0 || index >= len(state.Databases) {
			return fmt.Errorf("out of range")
		}
		state.Databases = append(state.Databases[:index], state.Databases[index+1:]...)

		reindexed := make(map[int]*driver.MultiSchema, len(state.Schemas))
		for i := range state.Databases {
			state.Databases[i].ID = i
		}
		for oldIdx, schema := range state.Schemas {
			if oldIdx < index {
				reindexed[oldIdx] = schema
			} else if oldIdx > index {
				reindexed[oldIdx-1] = schema
			}
		}
		state.Schemas = reindexed
		projectStates[projectName] = state
		syncProjects()
		return nil
	}

	return NewServer(fstest.MapFS{}, projectStates[currentProject], reloadFunc, selectFunc, addFunc, deleteFunc)
}

func TestHandleProjects(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ProjectsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.CurrentProject != "Alpha" {
		t.Fatalf("expected current project Alpha, got %q", resp.CurrentProject)
	}
	if len(resp.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(resp.Projects))
	}
}

func TestHandleSelectProject(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/select", strings.NewReader(`{"name":"Beta"}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/databases", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var dbs []DatabaseInfo
	if err := json.NewDecoder(rec.Body).Decode(&dbs); err != nil {
		t.Fatalf("decode dbs: %v", err)
	}
	if len(dbs) != 1 || dbs[0].Name != "Warehouse" {
		t.Fatalf("expected Beta project databases, got %#v", dbs)
	}
}

func TestHandleDatabases(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodGet, "/api/databases", nil)
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
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected wildcard CORS header, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestHandleSchemaOK(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodGet, "/api/databases/0/schema", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var multi driver.MultiSchema
	if err := json.NewDecoder(rec.Body).Decode(&multi); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(multi.Databases) != 1 || len(multi.Databases[0].Tables) != 1 {
		t.Fatalf("expected one schema table, got %#v", multi)
	}
}

func TestHandleSchemaBrokenDB(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodGet, "/api/databases/1/schema", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestHandleSchemaNotFound(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodGet, "/api/databases/99/schema", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleReload(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodPost, "/api/reload", nil)
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
		t.Fatalf("expected status ok, got %v", result["status"])
	}
}

func TestHandleAddDatabase(t *testing.T) {
	srv := testServer()

	body := `{"name":"New DB","driver":"sqlite","path":"/tmp/new.db"}`
	req := httptest.NewRequest(http.MethodPost, "/api/databases", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/databases", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var dbs []DatabaseInfo
	if err := json.NewDecoder(rec.Body).Decode(&dbs); err != nil {
		t.Fatalf("decode dbs: %v", err)
	}
	if len(dbs) != 3 || dbs[2].Name != "New DB" {
		t.Fatalf("expected new database in active project, got %#v", dbs)
	}
}

func TestHandleDeleteDatabase(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodDelete, "/api/databases/0", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/databases", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var dbs []DatabaseInfo
	if err := json.NewDecoder(rec.Body).Decode(&dbs); err != nil {
		t.Fatalf("decode dbs: %v", err)
	}
	if len(dbs) != 1 || dbs[0].Name != "Broken DB" || dbs[0].ID != 0 {
		t.Fatalf("unexpected databases after delete: %#v", dbs)
	}
}

func TestHandleTopology(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodGet, "/api/topology", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var repl []ReplicationInfo
	if err := json.NewDecoder(rec.Body).Decode(&repl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(repl) != 1 || repl[0].SourceName != "App DB" {
		t.Fatalf("unexpected topology payload: %#v", repl)
	}
}

func TestHandleTopologyReport(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest(http.MethodGet, "/api/topology/report", nil)
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

	req := httptest.NewRequest(http.MethodOptions, "/api/databases", nil)
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
