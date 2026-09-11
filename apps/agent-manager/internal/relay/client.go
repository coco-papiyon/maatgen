package relay

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// ClientOptions configures a lower node's outbound relay connection
// (Decision 1).
type ClientOptions struct {
	// UpstreamURL is the upper node's dedicated relay endpoint, e.g.
	// "ws://upper-host:3101/api/relay/connect".
	UpstreamURL string
	NodeID      string
	NodeName    string
	NodeToken   string
	// Handler is served over the relay session exactly as the lower node's
	// own loopback listener serves it (Decision 2): the same
	// server.New(...).Handler().
	Handler http.Handler
	Logger  *slog.Logger
	// OnConnected, if set, is called synchronously right after the
	// handshake succeeds — before dialOnce blocks serving the session — so
	// a caller (ClientSupervisor) can mark itself "connected" the moment it
	// actually is, rather than only after dialOnce returns once the
	// connection has already ended.
	OnConnected func()
}

// RunClient dials UpstreamURL and, once connected, serves Handler over the
// resulting yamux session until ctx is done. It retries with exponential
// backoff (capped) on dial failure or disconnect and only returns once ctx
// is done.
func RunClient(ctx context.Context, opts ClientOptions) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	const (
		initialBackoff = time.Second
		maxBackoff     = 30 * time.Second
	)
	backoff := initialBackoff
	for ctx.Err() == nil {
		connected, err := dialOnce(ctx, opts, logger)
		if err != nil {
			logger.Warn("relay: upstream connection ended", "upstream", opts.UpstreamURL, "error", err)
		}
		if connected {
			backoff = initialBackoff
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if !connected {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// dialOnce performs one connection attempt. connected reports whether the
// WebSocket/yamux handshake succeeded, regardless of how the session later
// ended, so RunClient can tell a failed dial (grow backoff) apart from a
// connection that ran and then dropped (retry promptly).
func dialOnce(ctx context.Context, opts ClientOptions, logger *slog.Logger) (connected bool, err error) {
	header := http.Header{}
	header.Set(HeaderNodeID, opts.NodeID)
	if opts.NodeName != "" {
		header.Set(HeaderNodeName, opts.NodeName)
	}
	if opts.NodeToken != "" {
		header.Set(HeaderNodeToken, opts.NodeToken)
	}

	conn, _, dialErr := websocket.Dial(ctx, opts.UpstreamURL, &websocket.DialOptions{HTTPHeader: header})
	if dialErr != nil {
		return false, dialErr
	}

	connCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	netConn := websocket.NetConn(connCtx, conn, websocket.MessageBinary)

	session, sessErr := yamux.Client(netConn, yamux.DefaultConfig())
	if sessErr != nil {
		conn.Close(websocket.StatusInternalError, "yamux session failed")
		return false, sessErr
	}
	defer session.Close()
	logger.Info("relay: connected to upstream", "upstream", opts.UpstreamURL, "node", opts.NodeID)
	if opts.OnConnected != nil {
		opts.OnConnected()
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- http.Serve(session, opts.Handler) }()

	select {
	case <-ctx.Done():
		session.Close()
		<-serveErr
		return true, nil
	case err := <-serveErr:
		return true, err
	}
}
