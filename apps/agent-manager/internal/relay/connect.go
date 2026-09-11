package relay

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// Header names a lower node sets on its /api/relay/connect WebSocket
// upgrade request to identify itself (Decision 3: no JSON handshake, just
// standard HTTP headers).
const (
	HeaderNodeID    = "X-Maatgen-Node-Id"
	HeaderNodeName  = "X-Maatgen-Node-Name"
	HeaderNodeToken = "X-Maatgen-Node-Token"
)

// ConnectHandler upgrades a lower node's outbound dial (Decision 1) into a
// yamux-multiplexed session (Decision 2) and registers it in registry until
// the session closes. It must be mounted only on the dedicated
// --relay-listen listener, never on the loopback-only browser-facing
// listener.
func ConnectHandler(registry *Registry, logger *slog.Logger) http.HandlerFunc {
	return connectHandler(registry, nil, logger)
}

func connectHandler(registry *Registry, upstreamHandler func() http.Handler, logger *slog.Logger) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.Header.Get(HeaderNodeID)
		if nodeID == "" {
			http.Error(w, HeaderNodeID+" header is required", http.StatusBadRequest)
			return
		}
		nodeName := r.Header.Get(HeaderNodeName)

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}

		// The relay session must outlive this handshake request; it is torn
		// down explicitly below, not by the request's own context.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		netConn := websocket.NetConn(ctx, conn, websocket.MessageBinary)

		session, err := yamux.Server(netConn, yamux.DefaultConfig())
		if err != nil {
			logger.Warn("relay: failed to start yamux session", "node", nodeID, "error", err)
			conn.Close(websocket.StatusInternalError, "yamux session failed")
			return
		}
		defer session.Close()

		node := registry.Connect(nodeID, nodeName, session)
		logger.Info("relay: node connected", "node", node.ID, "name", node.Name)
		defer func() {
			registry.Disconnect(nodeID, session)
			logger.Info("relay: node disconnected", "node", nodeID)
		}()

		if upstreamHandler != nil {
			if handler := upstreamHandler(); handler != nil {
				go func() {
					if err := http.Serve(session, handler); err != nil && !errors.Is(err, net.ErrClosed) {
						logger.Debug("relay: upper API server stopped", "node", nodeID, "error", err)
					}
				}()
			}
		}
		<-session.CloseChan()
	}
}
