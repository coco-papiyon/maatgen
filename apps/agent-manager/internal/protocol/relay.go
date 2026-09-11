package protocol

import "time"

// RelayNodeStatus is a lower node's state in the upper node's registry
// . pending is a node the upper node's Web UI created ahead of the
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

// UpstreamConfig is a lower node's own outbound connection setting: which
// upper node to dial, and under which node id/name/token. It is set from
// the lower node's own Web UI (a "Server" settings screen) and persisted to
// the tool config file so it survives restarts without CLI flags.
type UpstreamConfig struct {
	Enabled     bool   `json:"enabled"`
	UpstreamURL string `json:"upstreamUrl"`
	NodeID      string `json:"nodeId"`
	NodeName    string `json:"nodeName"`
	NodeToken   string `json:"nodeToken"`
}

// UpstreamConnectionState is the live state of the lower node's outbound
// connection attempt, shown on its own Server settings screen.
type UpstreamConnectionState string

const (
	UpstreamStateDisabled   UpstreamConnectionState = "disabled"
	UpstreamStateConnecting UpstreamConnectionState = "connecting"
	UpstreamStateConnected  UpstreamConnectionState = "connected"
	// UpstreamStateWaiting means the upper node has been unreachable long
	// enough that connection attempts have backed off to a long, quiet
	// interval (see internal/relay.ClientSupervisor) instead of retrying
	// every few seconds.
	UpstreamStateWaiting UpstreamConnectionState = "waiting"
)

type UpstreamStatus struct {
	Config          UpstreamConfig          `json:"config"`
	State           UpstreamConnectionState `json:"state"`
	LastError       string                  `json:"lastError,omitempty"`
	LastConnectedAt *time.Time              `json:"lastConnectedAt,omitempty"`
	NextAttemptAt   *time.Time              `json:"nextAttemptAt,omitempty"`
}
