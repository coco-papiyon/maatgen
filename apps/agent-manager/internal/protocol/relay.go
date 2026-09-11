package protocol

import "time"

// RelayNodeStatus is a lower node's state in the upper node's registry
// (ADR-009). pending is a node the upper node's Web UI created ahead of the
// lower node actually connecting; connected is a live relay session;
// disconnected is a node with connection history that is not currently
// connected.
type RelayNodeStatus string

const (
	RelayNodeStatusPending      RelayNodeStatus = "pending"
	RelayNodeStatusConnected    RelayNodeStatus = "connected"
	RelayNodeStatusDisconnected RelayNodeStatus = "disconnected"
)

// RelayNode describes one lower node from the upper node's point of view.
// The upper node's own local instance is not a RelayNode; it is represented
// by the fixed "local" id in API responses without going through the
// registry.
type RelayNode struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Status         RelayNodeStatus `json:"status"`
	CreatedAt      time.Time       `json:"createdAt"`
	ConnectedAt    *time.Time      `json:"connectedAt,omitempty"`
	LastSeenAt     *time.Time      `json:"lastSeenAt,omitempty"`
	StartupCommand string          `json:"startupCommand,omitempty"`
}

type CreateRelayNodeRequest struct {
	Name string `json:"name"`
}

type RelayNodeListResponse struct {
	Nodes []RelayNode `json:"nodes"`
}
