// Package api provides the HTTP server for DB Barrel.
package api

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"sync"

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

// ReplicationInfo is the public metadata for a replication link.
type ReplicationInfo struct {
	SourceName string `json:"sourceName"`
	TargetName string `json:"targetName"`
	Type       string `json:"type"`
	Details    string `json:"details,omitempty"`
}

// ReplicationManualLinkReport describes how one config replication entry was interpreted.
type ReplicationManualLinkReport struct {
	SourceInput    string `json:"sourceInput"`
	TargetInput    string `json:"targetInput"`
	TypeInput      string `json:"typeInput"`
	SourceResolved string `json:"sourceResolved,omitempty"`
	TargetResolved string `json:"targetResolved,omitempty"`
	TypeResolved   string `json:"typeResolved,omitempty"`
	Included       bool   `json:"included"`
	Reason         string `json:"reason,omitempty"`
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
	ConfiguredManualLinks       int `json:"configuredManualLinks"`
	ManualAcceptedLinks         int `json:"manualAcceptedLinks"`
	AutoDiscoveredLinks         int `json:"autoDiscoveredLinks"`
	MergedLinks                 int `json:"mergedLinks"`
	DroppedLinks                int `json:"droppedLinks"`
	EndpointErrors              int `json:"endpointErrors"`
}

// ReplicationReport contains a verbose snapshot of replication discovery.
type ReplicationReport struct {
	GeneratedAt       string                        `json:"generatedAt"`
	Summary           ReplicationSummary            `json:"summary"`
	ManualLinks       []ReplicationManualLinkReport `json:"manualLinks,omitempty"`
	PostgresEndpoints []ReplicationEndpointReport   `json:"postgresEndpoints,omitempty"`
	DroppedLinks      []ReplicationDroppedLink      `json:"droppedLinks,omitempty"`
	FinalLinks        []ReplicationInfo             `json:"finalLinks,omitempty"`
}

// ErrorResponse is a JSON error envelope.
type ErrorResponse struct {
	Error string `json:"error"`
}

// ReloadFunc is called by the reload endpoint to re-introspect all databases.
type ReloadFunc func() ([]DatabaseInfo, map[int]*driver.Schema, []ReplicationInfo, ReplicationReport)

// Server holds the HTTP handler configuration.
type Server struct {
	mu          sync.RWMutex
	mux         *http.ServeMux
	webFS       fs.FS
	databases   []DatabaseInfo
	schemas     map[int]*driver.Schema
	replication []ReplicationInfo
	replReport  ReplicationReport
	reloadFunc  ReloadFunc
}

// NewServer creates a new API server with pre-introspected database schemas.
func NewServer(webFS fs.FS, databases []DatabaseInfo, schemas map[int]*driver.Schema, replication []ReplicationInfo, replReport ReplicationReport, reload ReloadFunc) *Server {
	s := &Server{
		mux:         http.NewServeMux(),
		webFS:       webFS,
		databases:   databases,
		schemas:     schemas,
		replication: replication,
		replReport:  replReport,
		reloadFunc:  reload,
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/databases", s.handleDatabases)
	s.mux.HandleFunc("GET /api/databases/{id}/schema", s.handleSchema)
	s.mux.HandleFunc("GET /api/topology", s.handleTopology)
	s.mux.HandleFunc("GET /api/topology/report", s.handleTopologyReport)
	s.mux.HandleFunc("POST /api/reload", s.handleReload)

	// Serve embedded static files
	fileServer := http.FileServer(http.FS(s.webFS))
	s.mux.Handle("/", fileServer)
}

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, s.databases)
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
	log.Println("🔄 Reloading databases...")
	dbs, schemas, repl, replReport := s.reloadFunc()

	s.mu.Lock()
	s.databases = dbs
	s.schemas = schemas
	s.replication = repl
	s.replReport = replReport
	s.mu.Unlock()

	log.Printf("🔄 Reload complete: %d database(s)", len(dbs))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"databases": len(dbs),
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("error writing JSON response: %v", err)
	}
}
