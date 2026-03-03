package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"github.com/robotelu/db_barrel_2.0/internal/api"
	"github.com/robotelu/db_barrel_2.0/internal/config"
	"github.com/robotelu/db_barrel_2.0/internal/driver"

	// Register all database drivers.
	_ "github.com/robotelu/db_barrel_2.0/internal/driver"
)

//go:embed web/*
var webContent embed.FS

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	configPath := flag.String("config", "databases.json", "Path to database config JSON file")
	flag.Parse()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}
	log.Printf("📋 Loaded %d database(s) from %s", len(cfg.Databases), *configPath)

	// Introspect all databases at startup
	databases, schemas := introspectAll(cfg)
	replication := buildReplication(cfg)

	// Prepare web filesystem
	webFS, err := fs.Sub(webContent, "web")
	if err != nil {
		log.Fatalf("failed to create sub filesystem: %v", err)
	}

	// Reload function re-reads config and re-introspects
	cfgPath := *configPath
	reloadFunc := func() ([]api.DatabaseInfo, map[int]*driver.Schema, []api.ReplicationInfo) {
		newCfg, err := config.Load(cfgPath)
		if err != nil {
			log.Printf("❌ Reload failed to load config: %v", err)
			return databases, schemas, replication
		}
		log.Printf("📋 Reloaded %d database(s) from %s", len(newCfg.Databases), cfgPath)
		dbs, schs := introspectAll(newCfg)
		repl := buildReplication(newCfg)
		return dbs, schs, repl
	}

	srv := api.NewServer(webFS, databases, schemas, replication, reloadFunc)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("🛢  DB Barrel 2.0 starting on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func introspectAll(cfg *config.Config) ([]api.DatabaseInfo, map[int]*driver.Schema) {
	databases := make([]api.DatabaseInfo, len(cfg.Databases))
	schemas := make(map[int]*driver.Schema)

	for i, dbCfg := range cfg.Databases {
		info := api.DatabaseInfo{
			ID:     i,
			Name:   dbCfg.Name,
			Driver: dbCfg.Driver,
			Host:   dbCfg.Host,
			Port:   dbCfg.Port,
		}

		drv, err := driver.New(dbCfg.Driver)
		if err != nil {
			info.Status = "error"
			info.Error = err.Error()
			log.Printf("  ❌ [%d] %s — driver error: %v", i, dbCfg.Name, err)
			databases[i] = info
			continue
		}

		if err := drv.Connect(dbCfg.BuildDSN()); err != nil {
			info.Status = "error"
			info.Error = err.Error()
			log.Printf("  ❌ [%d] %s — connection error: %v", i, dbCfg.Name, err)
			databases[i] = info
			continue
		}

		schema, err := drv.Introspect()
		drv.Close()
		if err != nil {
			info.Status = "error"
			info.Error = err.Error()
			log.Printf("  ❌ [%d] %s — introspection error: %v", i, dbCfg.Name, err)
			databases[i] = info
			continue
		}

		info.Status = "ok"
		info.TableCount = len(schema.Tables)
		schemas[i] = schema
		log.Printf("  ✅ [%d] %s — %d tables", i, dbCfg.Name, info.TableCount)
		databases[i] = info
	}

	return databases, schemas
}

func buildReplication(cfg *config.Config) []api.ReplicationInfo {
	repl := make([]api.ReplicationInfo, 0, len(cfg.Replication))
	for _, r := range cfg.Replication {
		repl = append(repl, api.ReplicationInfo{
			SourceName: r.SourceName,
			TargetName: r.TargetName,
			Type:       r.Type,
		})
	}
	return repl
}
