package server

import (
	"encoding/json"
	"net/http"
	"time"
)

type HealthResponse struct {
	Status        string    `json:"status"`
	Version       string    `json:"version"`
	SchemaVersion int       `json:"schemaVersion"`
	Time          time.Time `json:"time"`
}

type Server struct {
	version string
	handler http.Handler
}

func New(version string, schemaVersion int) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{
			Status:        "ok",
			Version:       version,
			SchemaVersion: schemaVersion,
			Time:          time.Now().UTC(),
		})
	})

	return &Server{
		version: version,
		handler: mux,
	}
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
