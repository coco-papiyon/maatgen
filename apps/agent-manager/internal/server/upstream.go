package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// UpstreamStatusReader/UpstreamConfigSetter back this (lower) node's own
// "Server" settings screen : where to connect out to, and the live
// state of that connection. Unlike RelayController (this node acting as an
// upper node for others), these describe this node acting as a lower node.
type UpstreamStatusReader func(ctx context.Context) protocol.UpstreamStatus

type UpstreamConfigSetter func(ctx context.Context, config protocol.UpstreamConfig) (protocol.UpstreamStatus, error)

type UpstreamProxyProvider func() (http.Handler, bool)

func registerUpstreamRoutes(mux *http.ServeMux, statusReader UpstreamStatusReader, configSetter UpstreamConfigSetter, proxyProvider UpstreamProxyProvider) {
	if statusReader == nil || configSetter == nil {
		return
	}

	mux.Handle("GET /api/v1/upstream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, statusReader(r.Context()))
	}))

	mux.Handle("PUT /api/v1/upstream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request protocol.UpstreamConfig
		if err := readJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
			return
		}
		if request.Enabled && (request.UpstreamURL == "" || request.NodeID == "") {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "upstreamUrl and nodeId are required to enable the connection", nil)
			return
		}
		status, err := configSetter(r.Context(), request)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "upstream_config_failed", "upstream configuration could not be saved", nil)
			return
		}
		writeJSON(w, http.StatusOK, status)
	}))

	if proxyProvider != nil {
		mux.Handle("/api/upstream/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proxy, ok := proxyProvider()
			if !ok {
				writeAPIError(w, http.StatusServiceUnavailable, "upstream_disconnected", "the upstream server is not connected", nil)
				return
			}
			remainder := strings.TrimPrefix(r.URL.Path, "/api/upstream")
			if remainder == "" {
				remainder = "/"
			}
			r.URL.Path = remainder
			r.URL.RawPath = ""
			proxy.ServeHTTP(w, r)
		}))
	}
}
