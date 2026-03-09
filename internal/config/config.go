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

// ReplicationLink describes a replication relationship between two databases.
type ReplicationLink struct {
	SourceName string `json:"sourceName"`
	TargetName string `json:"targetName"`
	Type       string `json:"type"` // e.g. "streaming", "logical", "async"
	// Backward-compatible aliases accepted in some configs.
	Source          string `json:"source,omitempty"`
	Target          string `json:"target,omitempty"`
	ReplicationType string `json:"replicationType,omitempty"`
}

// Config is the top-level configuration structure.
type Config struct {
	Databases    []DatabaseConfig  `json:"databases"`
	Replication  []ReplicationLink `json:"replication,omitempty"`
	Replications []ReplicationLink `json:"replications,omitempty"`
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

	if len(cfg.Databases) == 0 {
		return nil, fmt.Errorf("config: no databases defined")
	}

	for i, db := range cfg.Databases {
		if db.Name == "" {
			return nil, fmt.Errorf("config: database[%d] missing name", i)
		}
		if db.Driver == "" {
			return nil, fmt.Errorf("config: database[%d] (%s) missing driver", i, db.Name)
		}
		drv := strings.ToLower(db.Driver)
		if drv == "sqlite" {
			if db.Path == "" {
				return nil, fmt.Errorf("config: database[%d] (%s) sqlite requires 'path'", i, db.Name)
			}
		} else {
			if db.Host == "" {
				return nil, fmt.Errorf("config: database[%d] (%s) missing host", i, db.Name)
			}
			if db.Database == "" {
				return nil, fmt.Errorf("config: database[%d] (%s) missing database", i, db.Name)
			}
		}
	}

	return &cfg, nil
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
