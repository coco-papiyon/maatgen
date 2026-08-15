package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/eventbroker"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestEventAPIAndWebSocketStream(t *testing.T) {
	session := protocol.AgentSession{
		ID:        "session-1",
		Agent:     protocol.AgentCodex,
		Workspace: "C:/workspace/project",
		Status:    protocol.SessionActive,
		CreatedAt: time.Date(2026, 8, 15, 5, 0, 0, 0, time.UTC),
	}
	event := protocol.SessionEvent{
		ID:            "event-1",
		SessionID:     session.ID,
		Sequence:      1,
		Timestamp:     session.CreatedAt,
		SchemaVersion: 1,
		Source:        protocol.EventSourceManager,
		Type:          "run_started",
		Data:          json.RawMessage(`{}`),
	}
	sessions := &fakeSessionReader{sessions: []protocol.AgentSession{session}}
	events := &fakeEventReader{events: []protocol.SessionEvent{event}}
	broker := eventbroker.New()
	config := testConfig()
	config.AllowedOrigins = []string{"http://localhost:5173"}
	config.EventSubscriber = broker
	httpServer := httptest.NewServer(New(config, sessions, events).Handler())
	defer httpServer.Close()

	eventRequest, err := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/sessions/session-1/events?afterSequence=0&limit=10", nil)
	if err != nil {
		t.Fatalf("create event request: %v", err)
	}
	eventRequest.Header.Set("Authorization", "Bearer test-token")
	eventResponse, err := http.DefaultClient.Do(eventRequest)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	defer eventResponse.Body.Close()
	if eventResponse.StatusCode != http.StatusOK {
		t.Fatalf("event status = %d", eventResponse.StatusCode)
	}
	var eventList eventListResponse
	if err := json.NewDecoder(eventResponse.Body).Decode(&eventList); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(eventList.Events) != 1 || eventList.Events[0].ID != event.ID {
		t.Fatalf("events = %#v", eventList.Events)
	}

	ticket := issueTicket(t, httpServer.URL)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws?sessionId=session-1&afterSequence=0"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader:   http.Header{"Origin": []string{"http://localhost:5173"}},
		Subprotocols: []string{webSocketProtocol, "ticket." + ticket},
	})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial websocket: %v (status %d)", err, status)
	}
	defer conn.CloseNow()

	var streamed protocol.SessionEvent
	if err := wsjson.Read(ctx, conn, &streamed); err != nil {
		t.Fatalf("read websocket event: %v", err)
	}
	if streamed.ID != event.ID || streamed.Sequence != 1 {
		t.Fatalf("streamed event = %#v", streamed)
	}

	second := event
	second.ID = "event-2"
	second.Sequence = 2
	second.Type = "assistant_message"
	events.append(second)
	broker.Publish(session.ID)
	if err := wsjson.Read(ctx, conn, &streamed); err != nil {
		t.Fatalf("read live websocket event: %v", err)
	}
	if streamed.ID != second.ID || streamed.Sequence != second.Sequence {
		t.Fatalf("live streamed event = %#v", streamed)
	}

	_, replayResponse, replayErr := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{webSocketProtocol, "ticket." + ticket},
	})
	if replayErr == nil {
		t.Fatal("reused ticket was accepted")
	}
	if replayResponse == nil || replayResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused ticket status = %#v, err = %v", replayResponse, replayErr)
	}
}

func issueTicket(t *testing.T, baseURL string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/ws-tickets", nil)
	if err != nil {
		t.Fatalf("create ticket request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("ticket status = %d", response.StatusCode)
	}
	var ticket wsTicketResponse
	if err := json.NewDecoder(response.Body).Decode(&ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}
	return ticket.Ticket
}

type fakeEventReader struct {
	mu     sync.RWMutex
	events []protocol.SessionEvent
}

func (f *fakeEventReader) ListEventsAfter(_ context.Context, sessionID string, afterSequence int64, limit int) ([]protocol.SessionEvent, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	result := make([]protocol.SessionEvent, 0, len(f.events))
	for _, event := range f.events {
		if event.SessionID == sessionID && event.Sequence > afterSequence {
			result = append(result, event)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (f *fakeEventReader) append(event protocol.SessionEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

var _ EventReader = (*fakeEventReader)(nil)
