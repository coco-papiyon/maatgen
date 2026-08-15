package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const webSocketProtocol = "maatgen.v1"

func serveEventWebSocket(
	w http.ResponseWriter,
	r *http.Request,
	config Config,
	tickets *ticketStore,
	sessions SessionReader,
	events EventReader,
	subscriber EventSubscriber,
) {
	if !originAllowed(r.Header.Get("Origin"), config.AllowedOrigins) {
		writeAPIError(w, http.StatusForbidden, "origin_not_allowed", "origin is not allowed", nil)
		return
	}
	ticket := ticketFromProtocols(r.Header.Values("Sec-WebSocket-Protocol"))
	if ticket == "" || !tickets.consume(ticket) {
		writeAPIError(w, http.StatusUnauthorized, "invalid_ticket", "a valid WebSocket ticket is required", nil)
		return
	}
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "sessionId is required", nil)
		return
	}
	if _, err := sessions.GetSession(r.Context(), sessionID); err != nil {
		writeStorageError(w, err)
		return
	}
	after, ok := parseBoundedInt64(w, r, "afterSequence", 0, 0)
	if !ok {
		return
	}
	if subscriber == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "event_stream_unavailable", "event stream is unavailable", nil)
		return
	}
	notifications, unsubscribe := subscriber.Subscribe(sessionID)
	defer unsubscribe()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		Subprotocols:       []string{webSocketProtocol},
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := conn.CloseRead(r.Context())
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		items, err := events.ListEventsAfter(ctx, sessionID, after, 200)
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, "event read failed")
			return
		}
		for _, event := range items {
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(writeCtx, conn, event)
			cancel()
			if err != nil {
				return
			}
			after = event.Sequence
		}
		if len(items) == 200 {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-notifications:
		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func ticketFromProtocols(values []string) string {
	for _, value := range values {
		for _, protocol := range strings.Split(value, ",") {
			protocol = strings.TrimSpace(protocol)
			if ticket, ok := strings.CutPrefix(protocol, "ticket."); ok {
				return ticket
			}
		}
	}
	return ""
}

func originAllowed(origin string, allowed []string) bool {
	if origin == "" {
		return true
	}
	for _, candidate := range allowed {
		if origin == candidate {
			return true
		}
	}
	return false
}
