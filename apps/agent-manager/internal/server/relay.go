package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/relay"
)

// RelayController is the ADR-009 node relay API surface exposed to the
// browser-facing (loopback) listener: listing/creating/deleting nodes and
// proxying to a connected one. It is satisfied by *relay.Service. The
// /api/relay/connect handshake itself is deliberately NOT part of this
// interface — it is mounted only on the separate --relay-listen listener
// (see cmd/agent-manager/main.go), never on the browser-facing one, so a
// lower node's inbound connection point stays independent of the loopback
// security policy for the rest of the API.
//
// Proxied requests under /api/nodes/{nodeId}/ never call back into this
// interface beyond NodeProxy: the returned http.Handler receives the
// request directly, so the lower node's own handler processes it exactly
// as it would a local request.
type RelayController interface {
	ListNodes(ctx context.Context) []protocol.RelayNode
	CreateNode(ctx context.Context, name string) (protocol.RelayNode, error)
	DeleteNode(ctx context.Context, id string) error
	NodeProxy(id string) (http.Handler, bool)
}

// localRelayNode is what GET /api/nodes reports for the upper node's own
// instance. It never lives in the relay registry: unlike a lower node it
// has no relay session, and the Web UI reaches it via the ordinary base URL
// with no /api/nodes/{id} prefix at all (Decision 5).
var localRelayNode = protocol.RelayNode{
	ID:        "local",
	Name:      "Local",
	Status:    protocol.RelayNodeStatusConnected,
	CreatedAt: time.Time{},
}

func registerRelayRoutes(mux *http.ServeMux, controller RelayController) {
	if controller == nil {
		return
	}

	mux.Handle("GET /api/nodes", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nodes := append([]protocol.RelayNode{localRelayNode}, controller.ListNodes(r.Context())...)
		writeJSON(w, http.StatusOK, protocol.RelayNodeListResponse{Nodes: nodes})
	}))

	mux.Handle("POST /api/nodes", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request protocol.CreateRelayNodeRequest
		if err := readJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
			return
		}
		node, err := controller.CreateNode(r.Context(), request.Name)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "node_create_failed", "node could not be created", nil)
			return
		}
		writeJSON(w, http.StatusCreated, node)
	}))

	mux.Handle("DELETE /api/nodes/{nodeId}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("nodeId")
		if id == "local" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "the local node cannot be deleted", nil)
			return
		}
		if err := controller.DeleteNode(r.Context(), id); err != nil {
			switch {
			case errors.Is(err, relay.ErrNodeNotFound):
				writeAPIError(w, http.StatusNotFound, "node_not_found", "node was not found", nil)
			case errors.Is(err, relay.ErrNodeConnected):
				writeAPIError(w, http.StatusConflict, "node_connected", "a connected node cannot be deleted; it will disconnect on its own once its process stops", nil)
			default:
				writeAPIError(w, http.StatusInternalServerError, "node_delete_failed", "node could not be deleted", nil)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	// Subtree pattern (trailing "/"): matches any method, any path under
	// /api/nodes/{nodeId}/. The prefix is stripped and the remainder
	// forwarded unchanged (Decision 2), so this single route proxies every
	// existing /api/v1/... and /ws endpoint without listing them again.
	mux.Handle("/api/nodes/{nodeId}/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("nodeId")
		proxy, ok := controller.NodeProxy(id)
		if !ok {
			writeAPIError(w, http.StatusNotFound, "node_not_found", "node is not connected", nil)
			return
		}
		prefix := "/api/nodes/" + id
		remainder := strings.TrimPrefix(r.URL.Path, prefix)
		if remainder == "" {
			remainder = "/"
		}
		r.URL.Path = remainder
		r.URL.RawPath = ""
		proxy.ServeHTTP(w, r)
	}))
}
