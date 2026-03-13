package catalog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/robotelu/db_barrel_2.0/internal/config"

	_ "github.com/robotelu/db_barrel_2.0/internal/driver"
)

// ProjectDefinition is one row from the projects catalog table.
type ProjectDefinition struct {
	Name   string
	Config *config.Config
}

// LoadProjects reads all project configs from the configured catalog database.
func LoadProjects(cfg config.ProjectCatalogConfig) ([]ProjectDefinition, error) {
	db, _, table, nameColumn, configColumn, err := openCatalogDB(cfg)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := fmt.Sprintf(
		"SELECT %s, %s FROM %s ORDER BY %s",
		nameColumn,
		configColumn,
		table,
		nameColumn,
	)
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("catalog: query projects: %w", err)
	}
	defer rows.Close()

	projects := make([]ProjectDefinition, 0)
	for rows.Next() {
		var name string
		var raw any
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, fmt.Errorf("catalog: scan project row: %w", err)
		}
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("catalog: encountered project row with empty name")
		}

		payload, err := rawJSONBytes(raw)
		if err != nil {
			return nil, fmt.Errorf("catalog: project %q invalid barrel_configs value: %w", name, err)
		}
		projectCfg, err := config.Parse(payload)
		if err != nil {
			return nil, fmt.Errorf("catalog: project %q invalid barrel_configs JSON: %w", name, err)
		}

		projects = append(projects, ProjectDefinition{
			Name:   name,
			Config: projectCfg,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate projects: %w", err)
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("catalog: no projects found in %s", table)
	}

	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
	})

	return projects, nil
}

// AppendDatabaseToProject adds a database entry to the selected project's barrel_configs JSON.
func AppendDatabaseToProject(cfg config.ProjectCatalogConfig, projectName string, dbCfg config.DatabaseConfig) error {
	return mutateProjectConfig(cfg, projectName, func(projectCfg *config.Config) error {
		projectCfg.Databases = append(projectCfg.Databases, dbCfg)
		return nil
	})
}

// RemoveDatabaseFromProject removes a database entry from the selected project's barrel_configs JSON.
func RemoveDatabaseFromProject(cfg config.ProjectCatalogConfig, projectName string, index int) error {
	return mutateProjectConfig(cfg, projectName, func(projectCfg *config.Config) error {
		if index < 0 || index >= len(projectCfg.Databases) {
			return fmt.Errorf("catalog: database index %d out of range for project %q", index, projectName)
		}
		projectCfg.Databases = append(projectCfg.Databases[:index], projectCfg.Databases[index+1:]...)
		return nil
	})
}

func mutateProjectConfig(cfg config.ProjectCatalogConfig, projectName string, mutate func(*config.Config) error) error {
	if strings.TrimSpace(projectName) == "" {
		return fmt.Errorf("catalog: project name is required")
	}

	db, driverName, table, nameColumn, configColumn, err := openCatalogDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	selectQuery := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s = %s",
		configColumn,
		table,
		nameColumn,
		placeholder(driverName, 1),
	)

	var raw any
	if err := db.QueryRow(selectQuery, projectName).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("catalog: project %q not found", projectName)
		}
		return fmt.Errorf("catalog: load project %q: %w", projectName, err)
	}

	payload, err := rawJSONBytes(raw)
	if err != nil {
		return fmt.Errorf("catalog: project %q invalid barrel_configs value: %w", projectName, err)
	}
	projectCfg, err := config.Parse(payload)
	if err != nil {
		return fmt.Errorf("catalog: project %q invalid barrel_configs JSON: %w", projectName, err)
	}
	if projectCfg.ProjectCatalog != nil {
		return fmt.Errorf("catalog: project %q barrel_configs must contain direct databases, not nested projectCatalog config", projectName)
	}

	if err := mutate(projectCfg); err != nil {
		return err
	}

	data, err := json.MarshalIndent(projectCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("catalog: encode updated project %q config: %w", projectName, err)
	}

	updateQuery := fmt.Sprintf(
		"UPDATE %s SET %s = %s WHERE %s = %s",
		table,
		configColumn,
		placeholder(driverName, 1),
		nameColumn,
		placeholder(driverName, 2),
	)
	result, err := db.Exec(updateQuery, string(data), projectName)
	if err != nil {
		return fmt.Errorf("catalog: save project %q: %w", projectName, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("catalog: save project %q rows affected: %w", projectName, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("catalog: project %q not updated", projectName)
	}

	return nil
}

func openCatalogDB(cfg config.ProjectCatalogConfig) (*sql.DB, string, string, string, string, error) {
	table := defaultString(cfg.ProjectsTable, "projects")
	nameColumn := defaultString(cfg.ProjectNameColumn, "name")
	configColumn := defaultString(cfg.BarrelConfigsColumn, "barrel_configs")

	for _, ident := range []string{table, nameColumn, configColumn} {
		if !isSafeIdentifier(ident) {
			return nil, "", "", "", "", fmt.Errorf("catalog: unsafe identifier %q", ident)
		}
	}

	driverName, err := sqlDriverName(cfg.Connection.Driver)
	if err != nil {
		return nil, "", "", "", "", err
	}

	db, err := sql.Open(driverName, cfg.Connection.BuildDSN())
	if err != nil {
		return nil, "", "", "", "", fmt.Errorf("catalog: connect: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, "", "", "", "", fmt.Errorf("catalog: ping: %w", err)
	}

	return db, driverName, table, nameColumn, configColumn, nil
}

func rawJSONBytes(value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return nil, fmt.Errorf("empty value")
	case []byte:
		return append([]byte(nil), v...), nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", value)
	}
}

func sqlDriverName(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "postgresql":
		return "postgres", nil
	case "mysql", "mariadb":
		return "mysql", nil
	case "sqlite":
		return "sqlite3", nil
	default:
		return "", fmt.Errorf("catalog: unsupported driver %q", name)
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func placeholder(driverName string, index int) string {
	if driverName == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func isSafeIdentifier(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}

	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for i, r := range part {
			if r == '_' {
				continue
			}
			if r >= '0' && r <= '9' {
				if i == 0 {
					return false
				}
				continue
			}
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				continue
			}
			return false
		}
	}
	return true
}
