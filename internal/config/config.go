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

// ReplicationConfig describes a manual replication link between configured databases.
type ReplicationConfig struct {
	SourceName string `json:"sourceName"`
	TargetName string `json:"targetName"`
	Type       string `json:"type,omitempty"`
	Details    string `json:"details,omitempty"`
}

// Config is the top-level configuration structure.
type Config struct {
	Databases   []DatabaseConfig    `json:"databases"`
	Replication []ReplicationConfig `json:"replication,omitempty"`
}

// Load reads and validates a JSON config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

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
	cfg.Databases = append(cfg.Databases, db)
	return Save(path, cfg)
}

// RemoveDatabaseAt removes a database entry by index and persists the config file.
func RemoveDatabaseAt(path string, index int) error {
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(cfg.Databases) {
		return fmt.Errorf("config: database index %d out of range", index)
	}
	cfg.Databases = append(cfg.Databases[:index], cfg.Databases[index+1:]...)
	return Save(path, cfg)
}

func validate(cfg *Config) error {
	if len(cfg.Databases) == 0 {
		return fmt.Errorf("config: no databases defined")
	}

	for i, db := range cfg.Databases {
		if db.Name == "" {
			return fmt.Errorf("config: database[%d] missing name", i)
		}
		if db.Driver == "" {
			return fmt.Errorf("config: database[%d] (%s) missing driver", i, db.Name)
		}
		drv := strings.ToLower(db.Driver)
		if drv == "sqlite" {
			if db.Path == "" {
				return fmt.Errorf("config: database[%d] (%s) sqlite requires 'path'", i, db.Name)
			}
		} else {
			if db.Host == "" {
				return fmt.Errorf("config: database[%d] (%s) missing host", i, db.Name)
			}
			if db.Database == "" {
				return fmt.Errorf("config: database[%d] (%s) missing database", i, db.Name)
			}
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
