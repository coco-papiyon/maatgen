// Package relay implements ADR-009: a lower Agent Manager node dials out to
// an upper Agent Manager node and, over the resulting yamux-multiplexed
// connection, exposes its own ordinary HTTP/WebSocket API so the upper
// node's Web UI can reach it through a per-node reverse proxy. There is no
// relay-specific request/response protocol beyond the initial handshake.
package relay

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/hashicorp/yamux"
)

var (
	// ErrNodeNotFound is returned by Delete for an unknown node id.
	ErrNodeNotFound = errors.New("relay: node not found")
	// ErrNodeConnected is returned by Delete for a node that is currently
	// connected; disconnect it first.
	ErrNodeConnected = errors.New("relay: node is connected")
)

type entry struct {
	node    protocol.RelayNode
	token   string
	session *yamux.Session
}

// Registry tracks lower nodes known to this (upper) Agent Manager: nodes
// pre-registered from the Web UI (Decision 3.1, "pending"), nodes with a
// live relay connection ("connected"), and nodes with connection history
// that are not currently connected ("disconnected"). It does not track the
// upper node's own local instance; callers represent that separately with a
// fixed "local" id.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*entry
	now     func() time.Time
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*entry), now: time.Now}
}

// CreatePending adds a node the Web UI is pre-registering (Decision 3.1) and
// returns it together with the token to embed in its startup command. The
// node stays "pending" until a lower node dials in with the same id.
func (r *Registry) CreatePending(name string) (protocol.RelayNode, string, error) {
	id, err := generateID()
	if err != nil {
		return protocol.RelayNode{}, "", err
	}
	token, err := generateToken()
	if err != nil {
		return protocol.RelayNode{}, "", err
	}
	node := protocol.RelayNode{
		ID:        id,
		Name:      name,
		Status:    protocol.RelayNodeStatusPending,
		CreatedAt: r.now().UTC(),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[id] = &entry{node: node, token: token}
	return node, token, nil
}

// Connect registers id as connected, reusing any pending/disconnected entry
// for it. An id that was never created via CreatePending is also accepted
// and registered fresh here (Decision 1/3: self-registration by a lower
// node started with an arbitrary --node-id remains supported; UI
// pre-registration is a convenience, not the only path). name always
// overwrites the stored display name with what the connecting node reports,
// so the registry reflects what --node-name it was actually started with.
func (r *Registry) Connect(id, name string, session *yamux.Session) protocol.RelayNode {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	e, ok := r.entries[id]
	if !ok {
		e = &entry{node: protocol.RelayNode{ID: id, CreatedAt: now}}
		r.entries[id] = e
	}
	if name != "" {
		e.node.Name = name
	}
	e.node.Status = protocol.RelayNodeStatusConnected
	e.node.ConnectedAt = &now
	e.node.LastSeenAt = &now
	e.session = session
	return e.node
}

// Disconnect marks id as disconnected. It is a no-op if id is unknown or its
// current session does not match session (an older, already-superseded
// connection reporting its own closure after a newer one replaced it).
func (r *Registry) Disconnect(id string, session *yamux.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.session != session {
		return
	}
	now := r.now().UTC()
	e.node.Status = protocol.RelayNodeStatusDisconnected
	e.node.LastSeenAt = &now
	e.session = nil
}

// Delete removes a pending or disconnected node's history. It refuses to
// remove a connected node (ErrNodeConnected); disconnect it first.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return ErrNodeNotFound
	}
	if e.node.Status == protocol.RelayNodeStatusConnected {
		return ErrNodeConnected
	}
	delete(r.entries, id)
	return nil
}

// List returns all known remote nodes (pending, connected, disconnected),
// newest-created first.
func (r *Registry) List() []protocol.RelayNode {
	r.mu.Lock()
	defer r.mu.Unlock()
	nodes := make([]protocol.RelayNode, 0, len(r.entries))
	for _, e := range r.entries {
		nodes = append(nodes, e.node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].CreatedAt.After(nodes[j].CreatedAt) })
	return nodes
}

// Session returns the live yamux session for a connected node.
func (r *Registry) Session(id string) (*yamux.Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.session == nil {
		return nil, false
	}
	return e.session, true
}

func generateID() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "node-" + hex.EncodeToString(random), nil
}

func generateToken() (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}
