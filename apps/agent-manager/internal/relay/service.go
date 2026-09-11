package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// Service is the upper-node side of ADR-009: it owns the node registry, the
// /api/relay/connect handshake, and per-node reverse proxies. It implements
// internal/server.RelayController.
type Service struct {
	registry     *Registry
	logger       *slog.Logger
	relayAddress string // host:port a lower node's --upstream-url should target, e.g. "upper-host:3101"
}

// NewService constructs a Service. relayAddress is embedded verbatim into
// generated startup commands (Decision 3.1); pass the address the upper
// node's --relay-listen is reachable at from a lower node's machine (not
// necessarily the bind address, which may be "0.0.0.0").
func NewService(relayAddress string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{registry: NewRegistry(), logger: logger, relayAddress: relayAddress}
}

func (s *Service) ConnectHandler() http.HandlerFunc {
	return ConnectHandler(s.registry, s.logger)
}

func (s *Service) ListNodes(_ context.Context) []protocol.RelayNode {
	return s.registry.List()
}

// CreateNode pre-registers a pending node (Decision 3.1) and returns it with
// a ready-to-copy startup command for the lower node's machine.
func (s *Service) CreateNode(_ context.Context, name string) (protocol.RelayNode, error) {
	node, token, err := s.registry.CreatePending(name)
	if err != nil {
		return protocol.RelayNode{}, err
	}
	node.StartupCommand = buildStartupCommand(s.relayAddress, node.ID, node.Name, token)
	return node, nil
}

func (s *Service) DeleteNode(_ context.Context, id string) error {
	return s.registry.Delete(id)
}

// NodeProxy returns a reverse proxy for a connected node, or false if it is
// not currently connected (pending, disconnected, or unknown).
func (s *Service) NodeProxy(id string) (http.Handler, bool) {
	session, ok := s.registry.Session(id)
	if !ok {
		return nil, false
	}
	return NewReverseProxy(session), true
}

func buildStartupCommand(relayAddress, nodeID, nodeName, token string) string {
	return fmt.Sprintf(
		"agent-manager --upstream-url ws://%s/api/relay/connect --node-id %s --node-name %q --node-token %s",
		relayAddress, nodeID, nodeName, token,
	)
}
