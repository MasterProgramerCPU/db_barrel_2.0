package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/robotelu/db_barrel_2.0/internal/api"
	"github.com/robotelu/db_barrel_2.0/internal/catalog"
	"github.com/robotelu/db_barrel_2.0/internal/config"
	"github.com/robotelu/db_barrel_2.0/internal/driver"

	// Register all database drivers.
	_ "github.com/robotelu/db_barrel_2.0/internal/driver"
)

//go:embed web/*
var webContent embed.FS

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	configPath := flag.String("config", "databases.json", "Path to DB Barrel config JSON file")
	flag.Parse()

	state, err := loadRuntimeState(*configPath, "")
	if err != nil {
		log.Fatalf("❌ Failed to initialize runtime state: %v", err)
	}

	webFS, err := fs.Sub(webContent, "web")
	if err != nil {
		log.Fatalf("failed to create sub filesystem: %v", err)
	}

	cfgPath := *configPath
	reloadFunc := func(preferredProject string) (api.ProjectState, error) {
		return loadRuntimeState(cfgPath, preferredProject)
	}
	selectProjectFunc := func(projectName string) (api.ProjectState, error) {
		return loadRuntimeState(cfgPath, projectName)
	}
	addDatabaseFunc := func(projectName string, dbCfg config.DatabaseConfig) error {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		if cfg.ProjectCatalog != nil {
			return catalog.AppendDatabaseToProject(*cfg.ProjectCatalog, projectName, dbCfg)
		}
		return config.AppendDatabase(cfgPath, dbCfg)
	}
	deleteDatabaseFunc := func(projectName string, index int) error {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		if cfg.ProjectCatalog != nil {
			return catalog.RemoveDatabaseFromProject(*cfg.ProjectCatalog, projectName, index)
		}
		return config.RemoveDatabaseAt(cfgPath, index)
	}

	srv := api.NewServer(webFS, state, reloadFunc, selectProjectFunc, addDatabaseFunc, deleteDatabaseFunc)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("🛢  DB Barrel 2.0 starting on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func loadRuntimeState(configPath, preferredProject string) (api.ProjectState, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return api.ProjectState{}, fmt.Errorf("load config: %w", err)
	}

	state, err := buildProjectState(cfg, preferredProject)
	if err != nil {
		return api.ProjectState{}, err
	}

	if cfg.ProjectCatalog != nil {
		log.Printf("📋 Loaded %d project(s) from metadata catalog; active project=%q", len(state.Projects), state.CurrentProject)
	} else {
		log.Printf("📋 Loaded legacy config with %d database(s)", len(cfg.Databases))
	}

	return state, nil
}

func buildProjectState(cfg *config.Config, preferredProject string) (api.ProjectState, error) {
	if cfg.ProjectCatalog == nil {
		databases, schemas := introspectAll(cfg)
		replication, replReport := buildReplicationWithReport(cfg)
		return api.ProjectState{
			Projects: []api.ProjectInfo{
				{Name: "Default Project", DatabaseCount: len(cfg.Databases)},
			},
			CurrentProject: "Default Project",
			Databases:      databases,
			Schemas:        schemas,
			Replication:    replication,
			ReplReport:     replReport,
		}, nil
	}

	projects, err := catalog.LoadProjects(*cfg.ProjectCatalog)
	if err != nil {
		return api.ProjectState{}, err
	}

	projectInfos := make([]api.ProjectInfo, 0, len(projects))
	for _, project := range projects {
		projectInfos = append(projectInfos, api.ProjectInfo{
			Name:          project.Name,
			DatabaseCount: len(project.Config.Databases),
		})
	}

	activeProject, activeCfg, err := pickProject(projects, preferredProject, cfg.ProjectCatalog.DefaultProject)
	if err != nil {
		return api.ProjectState{}, err
	}

	databases, schemas := introspectAll(activeCfg)
	replication, replReport := buildReplicationWithReport(activeCfg)

	return api.ProjectState{
		Projects:       projectInfos,
		CurrentProject: activeProject,
		Databases:      databases,
		Schemas:        schemas,
		Replication:    replication,
		ReplReport:     replReport,
	}, nil
}

func pickProject(projects []catalog.ProjectDefinition, preferredProject, defaultProject string) (string, *config.Config, error) {
	for _, candidate := range []string{preferredProject, defaultProject} {
		name := strings.TrimSpace(candidate)
		if name == "" {
			continue
		}
		for _, project := range projects {
			if strings.EqualFold(project.Name, name) {
				return project.Name, project.Config, nil
			}
		}
	}

	if len(projects) == 0 {
		return "", nil, fmt.Errorf("no projects available")
	}

	return projects[0].Name, projects[0].Config, nil
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

		schema, err := drv.Introspect()
		drv.Close()
		if err != nil {
			info.Status = "error"
			info.Error = err.Error()
			log.Printf("  ❌ [%d] %s — introspection error: %v", i, dbCfg.Name, err)
			databases[i] = info
			continue
		}

		selectedDBName := dbCfg.Database
		if selectedDBName == "" {
			selectedDBName = dbCfg.Name
		}
		if selectedDBName == "" {
			selectedDBName = "main"
		}

		for j := range schema.Tables {
			schema.Tables[j].Database = selectedDBName
		}

		multi := &driver.MultiSchema{
			Databases: []driver.DatabaseSchema{
				{
					Name:   selectedDBName,
					Tables: schema.Tables,
				},
			},
		}

		info.Status = "ok"
		info.TableCount = len(schema.Tables)
		schemas[i] = multi
		log.Printf("  ✅ [%d] %s — database=%s tables=%d", i, dbCfg.Name, selectedDBName, len(schema.Tables))
		databases[i] = info
	}

	return databases, schemas
}
