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
	replication, replReport := buildReplicationWithReport(cfg)

	// Prepare web filesystem
	webFS, err := fs.Sub(webContent, "web")
	if err != nil {
		log.Fatalf("failed to create sub filesystem: %v", err)
	}

	// Reload function re-reads config and re-introspects
	cfgPath := *configPath
	reloadFunc := func() ([]api.DatabaseInfo, map[int]*driver.MultiSchema, []api.ReplicationInfo, api.ReplicationReport) {
		newCfg, err := config.Load(cfgPath)
		if err != nil {
			log.Printf("❌ Reload failed to load config: %v", err)
			return databases, schemas, replication, replReport
		}
		log.Printf("📋 Reloaded %d database(s) from %s", len(newCfg.Databases), cfgPath)
		dbs, schs := introspectAll(newCfg)
		repl, report := buildReplicationWithReport(newCfg)
		return dbs, schs, repl, report
	}

	srv := api.NewServer(webFS, databases, schemas, replication, replReport, reloadFunc)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("🛢  DB Barrel 2.0 starting on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func introspectAll(cfg *config.Config) ([]api.DatabaseInfo, map[int]*driver.MultiSchema) {
	databases := make([]api.DatabaseInfo, len(cfg.Databases))
	schemas := make(map[int]*driver.MultiSchema)

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

		multi, err := drv.IntrospectAll()
		drv.Close()
		if err != nil {
			info.Status = "error"
			info.Error = err.Error()
			log.Printf("  ❌ [%d] %s — introspection error: %v", i, dbCfg.Name, err)
			databases[i] = info
			continue
		}

		totalTables := 0
		for _, db := range multi.Databases {
			totalTables += len(db.Tables)
		}

		info.Status = "ok"
		info.TableCount = totalTables
		schemas[i] = multi
		log.Printf("  ✅ [%d] %s — %d database(s), %d tables", i, dbCfg.Name, len(multi.Databases), totalTables)
		databases[i] = info
	}

	return databases, schemas
}
