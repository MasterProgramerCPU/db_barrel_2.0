// Package config handles loading database configurations from JSON files.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DatabaseConfig describes a single database connection.
type DatabaseConfig struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Database string `json:"database,omitempty"`
	Path     string `json:"path,omitempty"`    // For SQLite: file path
	SSLMode  string `json:"sslMode,omitempty"` // For PostgreSQL: disable, require, etc.
	Params   string `json:"params,omitempty"`  // Extra connection parameters
}

// ProjectCatalogConfig describes the metadata database that stores per-project DB Barrel configs.
type ProjectCatalogConfig struct {
	Connection          DatabaseConfig `json:"connection"`
	ProjectsTable       string         `json:"projectsTable,omitempty"`
	ProjectNameColumn   string         `json:"projectNameColumn,omitempty"`
	BarrelConfigsColumn string         `json:"barrelConfigsColumn,omitempty"`
	DefaultProject      string         `json:"defaultProject,omitempty"`
}

// ReplicationConfig describes a manual replication link between configured databases.
type ReplicationConfig struct {
	SourceName string `json:"sourceName"`
	TargetName string `json:"targetName"`
	Type       string `json:"type,omitempty"`
	Details    string `json:"details,omitempty"`
}

// Config is the top-level configuration structure.
type Config struct {
	Databases      []DatabaseConfig      `json:"databases,omitempty"`
	Replication    []ReplicationConfig   `json:"replication,omitempty"`
	ProjectCatalog *ProjectCatalogConfig `json:"projectCatalog,omitempty"`
}

// Load reads and validates a JSON config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	return Parse(data)
}

// Parse validates a JSON config document from memory.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Save validates and writes a config file as indented JSON.
func Save(path string, cfg *Config) error {
	if err := validate(cfg); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// AppendDatabase adds a database entry to the config file and persists it.
func AppendDatabase(path string, db DatabaseConfig) error {
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if cfg.ProjectCatalog != nil {
		return fmt.Errorf("config: appending databases is not supported when projectCatalog is configured")
	}
	cfg.Databases = append(cfg.Databases, db)
	return Save(path, cfg)
}

// RemoveDatabaseAt removes a database entry by index and persists the config file.
func RemoveDatabaseAt(path string, index int) error {
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if cfg.ProjectCatalog != nil {
		return fmt.Errorf("config: removing databases is not supported when projectCatalog is configured")
	}
	if index < 0 || index >= len(cfg.Databases) {
		return fmt.Errorf("config: database index %d out of range", index)
	}
	cfg.Databases = append(cfg.Databases[:index], cfg.Databases[index+1:]...)
	return Save(path, cfg)
}

func validate(cfg *Config) error {
	if cfg.ProjectCatalog != nil {
		if err := validateDatabaseConfig(cfg.ProjectCatalog.Connection, "config: projectCatalog.connection"); err != nil {
			return err
		}
		if strings.TrimSpace(cfg.ProjectCatalog.ProjectsTable) == "" {
			cfg.ProjectCatalog.ProjectsTable = "projects"
		}
		if strings.TrimSpace(cfg.ProjectCatalog.ProjectNameColumn) == "" {
			cfg.ProjectCatalog.ProjectNameColumn = "name"
		}
		if strings.TrimSpace(cfg.ProjectCatalog.BarrelConfigsColumn) == "" {
			cfg.ProjectCatalog.BarrelConfigsColumn = "barrel_configs"
		}
		return nil
	}

	if len(cfg.Databases) == 0 {
		return fmt.Errorf("config: no databases defined")
	}

	for i, db := range cfg.Databases {
		if err := validateDatabaseConfig(db, fmt.Sprintf("config: database[%d]", i)); err != nil {
			return err
		}
	}

	for i, link := range cfg.Replication {
		if strings.TrimSpace(link.SourceName) == "" {
			return fmt.Errorf("config: replication[%d] missing sourceName", i)
		}
		if strings.TrimSpace(link.TargetName) == "" {
			return fmt.Errorf("config: replication[%d] missing targetName", i)
		}
	}

	return nil
}

func validateDatabaseConfig(db DatabaseConfig, scope string) error {
	if db.Name == "" && scope != "config: projectCatalog.connection" {
		return fmt.Errorf("%s missing name", scope)
	}
	if db.Driver == "" {
		return fmt.Errorf("%s missing driver", scope)
	}
	drv := strings.ToLower(db.Driver)
	if drv == "sqlite" {
		if db.Path == "" {
			return fmt.Errorf("%s sqlite requires 'path'", scope)
		}
		return nil
	}
	if db.Host == "" {
		return fmt.Errorf("%s missing host", scope)
	}
	if db.Database == "" {
		return fmt.Errorf("%s missing database", scope)
	}
	return nil
}

// BuildDSN constructs a driver-specific DSN string from the config fields.
func (db *DatabaseConfig) BuildDSN() string {
	drv := strings.ToLower(db.Driver)

	switch drv {
	case "sqlite":
		return db.Path

	case "postgresql":
		// postgres://user:password@host:port/database?sslmode=disable
		dsn := "postgres://"
		if db.User != "" {
			dsn += db.User
			if db.Password != "" {
				dsn += ":" + db.Password
			}
			dsn += "@"
		}
		dsn += db.Host
		if db.Port > 0 {
			dsn += fmt.Sprintf(":%d", db.Port)
		}
		dsn += "/" + db.Database
		params := []string{}
		if db.SSLMode != "" {
			params = append(params, "sslmode="+db.SSLMode)
		}
		if db.Params != "" {
			params = append(params, db.Params)
		}
		if len(params) > 0 {
			dsn += "?" + strings.Join(params, "&")
		}
		return dsn

	case "mysql", "mariadb":
		// user:password@tcp(host:port)/database?params
		dsn := ""
		if db.User != "" {
			dsn += db.User
			if db.Password != "" {
				dsn += ":" + db.Password
			}
			dsn += "@"
		}
		host := db.Host
		if db.Port > 0 {
			host = fmt.Sprintf("%s:%d", host, db.Port)
		}
		dsn += fmt.Sprintf("tcp(%s)", host)
		dsn += "/" + db.Database
		if db.Params != "" {
			dsn += "?" + db.Params
		}
		return dsn

	default:
		// Fallback: try postgres-style
		return fmt.Sprintf("%s@%s/%s", db.User, db.Host, db.Database)
	}
}
