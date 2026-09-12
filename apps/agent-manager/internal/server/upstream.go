package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// ErrUpstreamNotFound is returned by UpstreamUpdater/UpstreamDeleter for an
// unknown upstream id.
var ErrUpstreamNotFound = errors.New("upstream not found")

// UpstreamLister/UpstreamCreator/UpstreamUpdater/UpstreamDeleter back this
// (lower) node's own "Server" settings screen: which upper nodes to connect
// out to, and each one's live connection state. A lower node may configure
// several of these at once. Unlike RelayController (this node acting as an
// upper node for others), these describe this node acting as a lower node.
type UpstreamLister func(ctx context.Context) []protocol.UpstreamStatus

type UpstreamCreator func(ctx context.Context, config protocol.UpstreamConfig) (protocol.UpstreamStatus, error)

type UpstreamUpdater func(ctx context.Context, id string, config protocol.UpstreamConfig) (protocol.UpstreamStatus, error)

type UpstreamDeleter func(ctx context.Context, id string) error

type UpstreamProxyProvider func(id string) (http.Handler, bool)

func registerUpstreamRoutes(mux *http.ServeMux, lister UpstreamLister, creator UpstreamCreator, updater UpstreamUpdater, deleter UpstreamDeleter, proxyProvider UpstreamProxyProvider) {
	if lister == nil || creator == nil || updater == nil || deleter == nil {
		return
	}

	mux.Handle("GET /api/v1/upstreams", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, protocol.UpstreamStatusListResponse{Upstreams: lister(r.Context())})
	}))

	mux.Handle("POST /api/v1/upstreams", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request protocol.UpstreamConfig
		if err := readJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
			return
		}
		if request.Enabled && (request.UpstreamURL == "" || request.NodeID == "") {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "upstreamUrl and nodeId are required to enable the connection", nil)
			return
		}
		status, err := creator(r.Context(), request)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "upstream_create_failed", "upstream server could not be added", nil)
			return
		}
		writeJSON(w, http.StatusCreated, status)
	}))

	mux.Handle("PUT /api/v1/upstreams/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var request protocol.UpstreamConfig
		if err := readJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
			return
		}
		if request.Enabled && (request.UpstreamURL == "" || request.NodeID == "") {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "upstreamUrl and nodeId are required to enable the connection", nil)
			return
		}
		status, err := updater(r.Context(), id, request)
		if err != nil {
			if errors.Is(err, ErrUpstreamNotFound) {
				writeAPIError(w, http.StatusNotFound, "upstream_not_found", "upstream server was not found", nil)
				return
			}
			writeAPIError(w, http.StatusInternalServerError, "upstream_config_failed", "upstream configuration could not be saved", nil)
			return
		}
		writeJSON(w, http.StatusOK, status)
	}))

	mux.Handle("DELETE /api/v1/upstreams/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := deleter(r.Context(), id); err != nil {
			if errors.Is(err, ErrUpstreamNotFound) {
				writeAPIError(w, http.StatusNotFound, "upstream_not_found", "upstream server was not found", nil)
				return
			}
			writeAPIError(w, http.StatusInternalServerError, "upstream_delete_failed", "upstream server could not be deleted", nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	if proxyProvider != nil {
		mux.Handle("/api/upstreams/{id}/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			proxy, ok := proxyProvider(id)
			if !ok {
				writeAPIError(w, http.StatusServiceUnavailable, "upstream_disconnected", "the upstream server is not connected", nil)
				return
			}
			prefix := "/api/upstreams/" + id
			remainder := strings.TrimPrefix(r.URL.Path, prefix)
			if remainder == "" {
				remainder = "/"
			}
			r.URL.Path = remainder
			r.URL.RawPath = ""
			proxy.ServeHTTP(w, r)
		}))
	}
}
