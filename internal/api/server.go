// Package api provides the HTTP server for DB Barrel.
package api

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/robotelu/db_barrel_2.0/internal/config"
	"github.com/robotelu/db_barrel_2.0/internal/driver"
)

// DatabaseInfo is the public metadata for a database (no DSN exposed).
type DatabaseInfo struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	TableCount int    `json:"tableCount"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
}

// ProjectInfo is the public metadata for a project.
type ProjectInfo struct {
	Name          string `json:"name"`
	DatabaseCount int    `json:"databaseCount"`
}

// ProjectsResponse contains the available project names and the active project.
type ProjectsResponse struct {
	CurrentProject string        `json:"currentProject"`
	Projects       []ProjectInfo `json:"projects"`
}

// ProjectSelectionRequest selects a project by name.
type ProjectSelectionRequest struct {
	Name string `json:"name"`
}

// ReplicationInfo is the public metadata for a replication link.
type ReplicationInfo struct {
	SourceName string `json:"sourceName"`
	TargetName string `json:"targetName"`
	Type       string `json:"type"`
	Details    string `json:"details,omitempty"`
}

// ReplicationEndpointReport contains per-endpoint discovery diagnostics.
type ReplicationEndpointReport struct {
	Name                          string   `json:"name"`
	Host                          string   `json:"host,omitempty"`
	Port                          int      `json:"port,omitempty"`
	Database                      string   `json:"database,omitempty"`
	ConnectOK                     bool     `json:"connectOK"`
	PingOK                        bool     `json:"pingOK"`
	InRecoveryKnown               bool     `json:"inRecoveryKnown"`
	InRecovery                    bool     `json:"inRecovery"`
	PrimaryConnInfoSeen           bool     `json:"primaryConnInfoSeen"`
	WalReceiverConnInfoSeen       bool     `json:"walReceiverConnInfoSeen"`
	StreamingLinksDetected        int      `json:"streamingLinksDetected"`
	LogicalSubscriptionsScanned   int      `json:"logicalSubscriptionsScanned"`
	LogicalSubscriptionsMatched   int      `json:"logicalSubscriptionsMatched"`
	LogicalSubscriptionsUnmatched int      `json:"logicalSubscriptionsUnmatched"`
	Errors                        []string `json:"errors,omitempty"`
	Notes                         []string `json:"notes,omitempty"`
}

// ReplicationDroppedLink describes a link dropped during normalization/deduping.
type ReplicationDroppedLink struct {
	SourceName string `json:"sourceName,omitempty"`
	TargetName string `json:"targetName,omitempty"`
	Type       string `json:"type,omitempty"`
	Reason     string `json:"reason"`
}

// ReplicationSummary aggregates topology build stats.
type ReplicationSummary struct {
	ConfiguredDatabases         int `json:"configuredDatabases"`
	ConfiguredPostgresDatabases int `json:"configuredPostgresDatabases"`
	AutoDiscoveredLinks         int `json:"autoDiscoveredLinks"`
	MergedLinks                 int `json:"mergedLinks"`
	DroppedLinks                int `json:"droppedLinks"`
	EndpointErrors              int `json:"endpointErrors"`
}

// ReplicationReport contains a verbose snapshot of replication discovery.
type ReplicationReport struct {
	GeneratedAt       string                      `json:"generatedAt"`
	Summary           ReplicationSummary          `json:"summary"`
	PostgresEndpoints []ReplicationEndpointReport `json:"postgresEndpoints,omitempty"`
	DroppedLinks      []ReplicationDroppedLink    `json:"droppedLinks,omitempty"`
	FinalLinks        []ReplicationInfo           `json:"finalLinks,omitempty"`
}

// ErrorResponse is a JSON error envelope.
type ErrorResponse struct {
	Error string `json:"error"`
}

// ProjectState is the active in-memory view served to the UI.
type ProjectState struct {
	Projects       []ProjectInfo
	CurrentProject string
	Databases      []DatabaseInfo
	Schemas        map[int]*driver.MultiSchema
	Replication    []ReplicationInfo
	ReplReport     ReplicationReport
}

// ReloadFunc refreshes the project catalog and active project.
type ReloadFunc func(preferredProject string) (ProjectState, error)

// SelectProjectFunc switches the active project.
type SelectProjectFunc func(string) (ProjectState, error)

// AddDatabaseFunc appends a database entry to the current project's config.
type AddDatabaseFunc func(projectName string, dbCfg config.DatabaseConfig) error

// DeleteDatabaseFunc removes a database entry from the current project's config.
type DeleteDatabaseFunc func(projectName string, index int) error

// Server holds the HTTP handler configuration.
type Server struct {
	mu                sync.RWMutex
	mux               *http.ServeMux
	webFS             fs.FS
	projects          []ProjectInfo
	currentProject    string
	databases         []DatabaseInfo
	schemas           map[int]*driver.MultiSchema
	replication       []ReplicationInfo
	replReport        ReplicationReport
	reloadFunc        ReloadFunc
	selectProjectFunc SelectProjectFunc
	addDBFunc         AddDatabaseFunc
	deleteDBFunc      DeleteDatabaseFunc
}

// NewServer creates a new API server with an initial project state.
func NewServer(webFS fs.FS, initial ProjectState, reload ReloadFunc, selectProject SelectProjectFunc, addDB AddDatabaseFunc, deleteDB DeleteDatabaseFunc) *Server {
	s := &Server{
		mux:               http.NewServeMux(),
		webFS:             webFS,
		reloadFunc:        reload,
		selectProjectFunc: selectProject,
		addDBFunc:         addDB,
		deleteDBFunc:      deleteDB,
	}
	s.applyState(initial)
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	applyCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/projects", s.handleProjects)
	s.mux.HandleFunc("POST /api/projects/select", s.handleSelectProject)
	s.mux.HandleFunc("GET /api/databases", s.handleDatabases)
	s.mux.HandleFunc("POST /api/databases", s.handleAddDatabase)
	s.mux.HandleFunc("DELETE /api/databases/{id}", s.handleDeleteDatabase)
	s.mux.HandleFunc("GET /api/databases/{id}/schema", s.handleSchema)
	s.mux.HandleFunc("GET /api/topology", s.handleTopology)
	s.mux.HandleFunc("GET /api/topology/report", s.handleTopologyReport)
	s.mux.HandleFunc("POST /api/reload", s.handleReload)

	fileServer := http.FileServer(http.FS(s.webFS))
	s.mux.Handle("/", fileServer)
}

func (s *Server) applyState(state ProjectState) {
	s.projects = append([]ProjectInfo(nil), state.Projects...)
	s.currentProject = state.CurrentProject
	s.databases = append([]DatabaseInfo(nil), state.Databases...)
	s.schemas = state.Schemas
	if s.schemas == nil {
		s.schemas = make(map[int]*driver.MultiSchema)
	}
	s.replication = append([]ReplicationInfo(nil), state.Replication...)
	s.replReport = state.ReplReport
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, ProjectsResponse{
		CurrentProject: s.currentProject,
		Projects:       s.projects,
	})
}

func (s *Server) handleSelectProject(w http.ResponseWriter, r *http.Request) {
	if s.selectProjectFunc == nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "project selection not configured"})
		return
	}

	var req ProjectSelectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "project name is required"})
		return
	}

	state, err := s.selectProjectFunc(req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	s.mu.Lock()
	s.applyState(state)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"project":       s.currentProject,
		"databaseCount": len(s.databases),
	})
}

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, s.databases)
}

func (s *Server) handleAddDatabase(w http.ResponseWriter, r *http.Request) {
	if s.addDBFunc == nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "database creation not configured"})
		return
	}

	var dbCfg config.DatabaseConfig
	if err := json.NewDecoder(r.Body).Decode(&dbCfg); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	s.mu.RLock()
	projectName := s.currentProject
	s.mu.RUnlock()

	if err := s.addDBFunc(projectName, dbCfg); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	if err := s.reloadIntoCurrentProject(); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status":        "ok",
		"project":       s.currentProject,
		"databaseCount": len(s.databases),
	})
}

func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	if s.deleteDBFunc == nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "database deletion not configured"})
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid database id"})
		return
	}

	s.mu.RLock()
	projectName := s.currentProject
	s.mu.RUnlock()

	if err := s.deleteDBFunc(projectName, id); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	if err := s.reloadIntoCurrentProject(); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"project":       s.currentProject,
		"databaseCount": len(s.databases),
	})
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 || id >= len(s.databases) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "database not found"})
		return
	}

	schema, ok := s.schemas[id]
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{
			Error: "schema not available: " + s.databases[id].Error,
		})
		return
	}

	writeJSON(w, http.StatusOK, schema)
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, s.replication)
}

func (s *Server) handleTopologyReport(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, s.replReport)
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if s.reloadFunc == nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "reload not configured"})
		return
	}

	s.mu.RLock()
	preferredProject := s.currentProject
	s.mu.RUnlock()

	log.Printf("🔄 Reloading project catalog (preferred project=%q)...", preferredProject)
	state, err := s.reloadFunc(preferredProject)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	s.mu.Lock()
	s.applyState(state)
	s.mu.Unlock()

	log.Printf("🔄 Reload complete: project=%q databases=%d", s.currentProject, len(s.databases))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"project":       s.currentProject,
		"projects":      len(s.projects),
		"databaseCount": len(s.databases),
	})
}

func (s *Server) reloadIntoCurrentProject() error {
	if s.reloadFunc == nil {
		return nil
	}

	s.mu.RLock()
	preferredProject := s.currentProject
	s.mu.RUnlock()

	state, err := s.reloadFunc(preferredProject)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.applyState(state)
	s.mu.Unlock()
	return nil
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("error writing JSON response: %v", err)
	}
}

func applyCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	w.Header().Set("Vary", "Origin")
	w.Header().Add("Vary", "Access-Control-Request-Method")
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")

	reqHeaders := r.Header.Get("Access-Control-Request-Headers")
	if reqHeaders == "" {
		reqHeaders = "Content-Type, Authorization, X-Requested-With"
	}
	w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
	w.Header().Set("Access-Control-Expose-Headers", "Content-Type")
}
