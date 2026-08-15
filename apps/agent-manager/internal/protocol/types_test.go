package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSessionEventFixture(t *testing.T) {
	content, err := os.ReadFile(fixturePath(t, "session-event.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var event SessionEvent
	if err := json.Unmarshal(content, &event); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	if event.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", event.SchemaVersion, SchemaVersion)
	}
	if event.Source != EventSourceCopilot {
		t.Fatalf("source = %q, want %q", event.Source, EventSourceCopilot)
	}
}

func TestProtocolFixturesDecode(t *testing.T) {
	tests := []struct {
		name   string
		target func() any
	}{
		{name: "agent-session.json", target: func() any { return &AgentSession{} }},
		{name: "create-session-request.json", target: func() any { return &CreateSessionRequest{} }},
		{name: "send-message-request.json", target: func() any { return &SendMessageRequest{} }},
		{name: "agent-run.json", target: func() any { return &AgentRun{} }},
		{name: "token-usage.json", target: func() any { return &TokenUsage{} }},
		{name: "session-event.json", target: func() any { return &SessionEvent{} }},
		{name: "change-set.json", target: func() any { return &ChangeSet{} }},
		{name: "api-error.json", target: func() any { return &APIErrorResponse{} }},
		{name: "session-list.json", target: func() any {
			return &struct {
				Sessions []AgentSession `json:"sessions"`
			}{}
		}},
		{name: "event-list.json", target: func() any {
			return &struct {
				Events []SessionEvent `json:"events"`
			}{}
		}},
		{name: "ws-ticket.json", target: func() any {
			return &struct {
				Ticket    string    `json:"ticket"`
				ExpiresAt time.Time `json:"expiresAt"`
			}{}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content, err := os.ReadFile(fixturePath(t, test.name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if err := json.Unmarshal(content, test.target()); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
		})
	}
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Join(
		filepath.Dir(filename),
		"..", "..", "..", "..",
		"packages", "protocol", "fixtures", name,
	)
}
