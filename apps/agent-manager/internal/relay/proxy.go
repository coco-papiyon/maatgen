package relay

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"

	"github.com/hashicorp/yamux"
)

// NewReverseProxy returns a reverse proxy that forwards every request to
// the node behind session unchanged: same path, same headers, same body
// (Decision 2 — no relay-specific request/response envelope). Callers are
// expected to have already stripped the "/api/nodes/{nodeId}" prefix from
// the request path before calling ServeHTTP, so what reaches the lower
// node's handler is identical to what a browser talking to it directly
// would send.
//
// net/http/httputil.ReverseProxy has special-cased Upgrade handling since Go
// 1.12: it tunnels a WebSocket (or other Connection: Upgrade) request over
// whatever connection Transport.DialContext returns. Because that dial goes
// through the same yamux session, the browser's /ws event stream is
// relayed by this same proxy with no extra code.
func NewReverseProxy(session *yamux.Session) *httputil.ReverseProxy {
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return session.Open()
		},
	}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Only scheme/host need to change; pr.Out.URL.Path is already a
			// copy of the caller's (already prefix-stripped) request path.
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "maatgen-node"
		},
		Transport: transport,
	}
}
