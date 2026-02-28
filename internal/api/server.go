// Package api provides the HTTP server for DB Barrel.
package api

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"

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
}

// ErrorResponse is a JSON error envelope.
type ErrorResponse struct {
	Error string `json:"error"`
}

// Server holds the HTTP handler configuration.
type Server struct {
	mux       *http.ServeMux
	webFS     fs.FS
	databases []DatabaseInfo
	schemas   map[int]*driver.Schema
}

// NewServer creates a new API server with pre-introspected database schemas.
func NewServer(webFS fs.FS, databases []DatabaseInfo, schemas map[int]*driver.Schema) *Server {
	s := &Server{
		mux:       http.NewServeMux(),
		webFS:     webFS,
		databases: databases,
		schemas:   schemas,
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

	// Serve embedded static files
	fileServer := http.FileServer(http.FS(s.webFS))
	s.mux.Handle("/", fileServer)
}

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.databases)
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
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

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("error writing JSON response: %v", err)
	}
}
