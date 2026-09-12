package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// aggregateTarget is one node this (upper) node can reach for the purposes
// of GET /api/sessions/all: either itself (proxy is nil, sessions come from
// the local SessionReader directly) or a connected node reached through the
// exact same proxy handler the browser would use via
// /api/nodes/{id}/... or /api/upstreams/{id}/... (Decision 2) — just
// invoked in-process instead of over HTTP from the browser.
type aggregateTarget struct {
	nodeID   string
	nodeName string
	proxy    http.Handler
}

func aggregateTargets(ctx context.Context, relayController RelayController, upstreamLister UpstreamLister, upstreamProxyProvider UpstreamProxyProvider) []aggregateTarget {
	targets := []aggregateTarget{{nodeID: "local", nodeName: "Local"}}
	if relayController != nil {
		for _, node := range relayController.ListNodes(ctx) {
			if node.Status != protocol.RelayNodeStatusConnected {
				continue
			}
			proxy, ok := relayController.NodeProxy(node.ID)
			if !ok {
				continue
			}
			targets = append(targets, aggregateTarget{nodeID: node.ID, nodeName: node.Name, proxy: proxy})
		}
	}
	if upstreamLister != nil && upstreamProxyProvider != nil {
		for _, status := range upstreamLister(ctx) {
			if status.State != protocol.UpstreamStateConnected || status.Config.ID == "" {
				continue
			}
			proxy, ok := upstreamProxyProvider(status.Config.ID)
			if !ok {
				continue
			}
			name := status.Config.NodeName
			if name == "" {
				name = status.Config.NodeID
			}
			// "upstream:{id}" matches the Web UI's own virtual node id
			// scheme (apps/web/src/nodes.ts upstreamNodeId), so a session's
			// nodeId in the aggregate response can be used directly to
			// select that same node in the selector.
			targets = append(targets, aggregateTarget{nodeID: "upstream:" + status.Config.ID, nodeName: name, proxy: proxy})
		}
	}
	return targets
}

// fetchRemoteSessions issues GET /api/v1/sessions against a remote node's
// existing proxy handler, exactly as the browser would through
// /api/nodes/{id}/... or /api/upstreams/{id}/..., except in-process: proxy
// already expects a request path relative to the target node (the prefix
// is stripped by the caller of NodeProxy/UpstreamProxyProvider, mirroring
// registerRelayRoutes and registerUpstreamRoutes).
func fetchRemoteSessions(proxy http.Handler, limit int, status protocol.SessionStatus) ([]protocol.AgentSession, error) {
	query := "/api/v1/sessions?limit=" + strconv.Itoa(limit)
	switch status {
	case protocol.SessionActive:
		query += "&status=active"
	case protocol.SessionClosed:
		query += "&status=closed"
	default:
		query += "&status=all"
	}
	req := httptest.NewRequest(http.MethodGet, query, nil)
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		return nil, fmt.Errorf("remote sessions request failed with status %d", recorder.Code)
	}
	var response sessionListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode remote sessions response: %w", err)
	}
	return response.Sessions, nil
}

// registerAggregateSessionRoute implements ADR-009 Decision 5.1: GET
// /api/sessions/all fans the existing per-node "GET /api/v1/sessions" out to
// every reachable node (this node, every connected downstream node, and
// every connected upstream node) and merges the results by createdAt. It
// reuses the exact same proxying this node already does for
// /api/nodes/{id}/... and /api/upstreams/{id}/... (Decision 2): no new wire
// format, just an in-process ServeHTTP through each node's existing proxy
// handler. One node failing to respond does not fail the whole request; its
// id is reported in unavailableNodes instead (Decision 5.1's "partial"
// tolerance).
func registerAggregateSessionRoute(mux *http.ServeMux, sessions SessionReader, relayController RelayController, upstreamLister UpstreamLister, upstreamProxyProvider UpstreamProxyProvider) {
	mux.Handle("GET /api/sessions/all", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, ok := parseBoundedInt(w, r, "limit", 100, 1, 500)
		if !ok {
			return
		}
		status, ok := parseSessionStatusFilter(w, r)
		if !ok {
			return
		}

		targets := aggregateTargets(r.Context(), relayController, upstreamLister, upstreamProxyProvider)

		type outcome struct {
			target   aggregateTarget
			sessions []protocol.AgentSession
			err      error
		}
		outcomes := make([]outcome, len(targets))
		var wg sync.WaitGroup
		for i, target := range targets {
			wg.Add(1)
			go func(i int, target aggregateTarget) {
				defer wg.Done()
				if target.proxy == nil {
					items, err := sessions.ListSessions(r.Context(), limit, nil, status)
					outcomes[i] = outcome{target: target, sessions: items, err: err}
					return
				}
				items, err := fetchRemoteSessions(target.proxy, limit, status)
				outcomes[i] = outcome{target: target, sessions: items, err: err}
			}(i, target)
		}
		wg.Wait()

		merged := make([]protocol.NodeScopedSession, 0, limit)
		unavailable := make([]string, 0)
		for _, o := range outcomes {
			if o.err != nil {
				unavailable = append(unavailable, o.target.nodeID)
				continue
			}
			for _, session := range o.sessions {
				merged = append(merged, protocol.NodeScopedSession{
					AgentSession: session,
					NodeID:       o.target.nodeID,
					NodeName:     o.target.nodeName,
				})
			}
		}
		sort.Slice(merged, func(i, j int) bool { return merged[i].CreatedAt.After(merged[j].CreatedAt) })
		if len(merged) > limit {
			merged = merged[:limit]
		}
		writeJSON(w, http.StatusOK, protocol.NodeScopedSessionListResponse{Sessions: merged, UnavailableNodes: unavailable})
	}))
}
