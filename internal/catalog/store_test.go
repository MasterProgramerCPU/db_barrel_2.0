package catalog

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/robotelu/db_barrel_2.0/internal/config"
)

func TestLoadProjectsSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "catalog.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE projects (
			name TEXT NOT NULL,
			barrel_configs TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create projects table: %v", err)
	}

	alphaJSON := `{"databases":[{"name":"AlphaDB","driver":"sqlite","path":"` + filepath.Join(tmpDir, "alpha.db") + `"}]}`
	betaJSON := `{"databases":[{"name":"BetaDB","driver":"sqlite","path":"` + filepath.Join(tmpDir, "beta.db") + `"}]}`
	if _, err := db.Exec(`INSERT INTO projects(name, barrel_configs) VALUES (?, ?), (?, ?)`, "Beta", betaJSON, "Alpha", alphaJSON); err != nil {
		t.Fatalf("insert projects: %v", err)
	}

	projects, err := LoadProjects(config.ProjectCatalogConfig{
		Connection: config.DatabaseConfig{
			Driver: "sqlite",
			Path:   dbPath,
		},
	})
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}

	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if projects[0].Name != "Alpha" || projects[1].Name != "Beta" {
		t.Fatalf("expected alphabetical project order, got %#v", projects)
	}
	if len(projects[0].Config.Databases) != 1 || projects[0].Config.Databases[0].Name != "AlphaDB" {
		t.Fatalf("unexpected parsed config: %#v", projects[0].Config)
	}
}

func TestLoadProjectsRejectsUnsafeIdentifiers(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "catalog.db")
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatalf("write sqlite file: %v", err)
	}

	_, err := LoadProjects(config.ProjectCatalogConfig{
		Connection: config.DatabaseConfig{
			Driver: "sqlite",
			Path:   dbPath,
		},
		ProjectsTable: "projects; DROP TABLE projects",
	})
	if err == nil {
		t.Fatal("expected unsafe identifier error")
	}
}

func TestAppendAndRemoveDatabaseFromProjectSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "catalog.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE projects (
			name TEXT NOT NULL,
			barrel_configs TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create projects table: %v", err)
	}

	initialJSON := `{"databases":[{"name":"AlphaDB","driver":"sqlite","path":"` + filepath.Join(tmpDir, "alpha.db") + `"}]}`
	if _, err := db.Exec(`INSERT INTO projects(name, barrel_configs) VALUES (?, ?)`, "Alpha", initialJSON); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	catalogCfg := config.ProjectCatalogConfig{
		Connection: config.DatabaseConfig{
			Driver: "sqlite",
			Path:   dbPath,
		},
	}

	if err := AppendDatabaseToProject(catalogCfg, "Alpha", config.DatabaseConfig{
		Name:   "BetaDB",
		Driver: "sqlite",
		Path:   filepath.Join(tmpDir, "beta.db"),
	}); err != nil {
		t.Fatalf("AppendDatabaseToProject: %v", err)
	}

	projects, err := LoadProjects(catalogCfg)
	if err != nil {
		t.Fatalf("LoadProjects after append: %v", err)
	}
	if len(projects[0].Config.Databases) != 2 {
		t.Fatalf("expected 2 databases after append, got %d", len(projects[0].Config.Databases))
	}

	if err := RemoveDatabaseFromProject(catalogCfg, "Alpha", 0); err != nil {
		t.Fatalf("RemoveDatabaseFromProject: %v", err)
	}

	projects, err = LoadProjects(catalogCfg)
	if err != nil {
		t.Fatalf("LoadProjects after remove: %v", err)
	}
	if len(projects[0].Config.Databases) != 1 || projects[0].Config.Databases[0].Name != "BetaDB" {
		t.Fatalf("unexpected databases after remove: %#v", projects[0].Config.Databases)
	}
}

func TestLoadProjectsAllowsEmptyBarrelConfigs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "catalog.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE projects (
			name TEXT NOT NULL,
			barrel_configs TEXT
		)
	`); err != nil {
		t.Fatalf("create projects table: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO projects(name, barrel_configs) VALUES (?, ?), (?, ?), (?, NULL)`, "Blank", "", "EmptyObject", "{}", "NullConfig"); err != nil {
		t.Fatalf("insert projects: %v", err)
	}

	projects, err := LoadProjects(config.ProjectCatalogConfig{
		Connection: config.DatabaseConfig{
			Driver: "sqlite",
			Path:   dbPath,
		},
	})
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}

	if len(projects) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(projects))
	}
	for _, project := range projects {
		if len(project.Config.Databases) != 0 {
			t.Fatalf("expected project %q to load with zero databases, got %#v", project.Name, project.Config.Databases)
		}
	}
}

func TestAppendDatabaseToEmptyProjectSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "catalog.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE projects (
			name TEXT NOT NULL,
			barrel_configs TEXT
		)
	`); err != nil {
		t.Fatalf("create projects table: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO projects(name, barrel_configs) VALUES (?, NULL)`, "EmptyProject"); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	catalogCfg := config.ProjectCatalogConfig{
		Connection: config.DatabaseConfig{
			Driver: "sqlite",
			Path:   dbPath,
		},
	}

	if err := AppendDatabaseToProject(catalogCfg, "EmptyProject", config.DatabaseConfig{
		Name:   "SeedDB",
		Driver: "sqlite",
		Path:   filepath.Join(tmpDir, "seed.db"),
	}); err != nil {
		t.Fatalf("AppendDatabaseToProject: %v", err)
	}

	projects, err := LoadProjects(catalogCfg)
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(projects) != 1 || len(projects[0].Config.Databases) != 1 || projects[0].Config.Databases[0].Name != "SeedDB" {
		t.Fatalf("unexpected loaded project after append: %#v", projects)
	}
}
