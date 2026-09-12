package protocol

// NodeScopedSession wraps AgentSession with the node it belongs to, for the
// cross-node aggregate GET /api/sessions/all (ADR-009 Decision 5.1). This
// wrapper exists only on that aggregate response; every other endpoint
// (including a single node's own GET /api/v1/sessions) keeps returning
// plain AgentSession, unchanged.
type NodeScopedSession struct {
	AgentSession
	NodeID   string `json:"nodeId"`
	NodeName string `json:"nodeName"`
}

// NodeScopedSessionListResponse is GET /api/sessions/all's body.
// UnavailableNodes lists node ids that failed to respond to this particular
// request; per Decision 5.1, one node's failure does not fail the whole
// aggregate — its sessions are simply missing from Sessions.
type NodeScopedSessionListResponse struct {
	Sessions         []NodeScopedSession `json:"sessions"`
	UnavailableNodes []string            `json:"unavailableNodes,omitempty"`
}
