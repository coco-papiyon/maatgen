package copilot

import (
	"encoding/json"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestParseCopilotEvents(t *testing.T) {
	tests := []struct{ line, eventType string }{
		{`{"type":"assistant.turn_start","data":{"sessionId":"d40fc21a-7d30-4e50-9bb0-c922aae7988f","turnId":"1"}}`, protocol.EventTypeRunStarted},
		{`{"type":"assistant.message","id":"event-1","data":{"messageId":"message-1","content":"Implemented it."}}`, protocol.EventTypeAssistantMessage},
		{`{"type":"assistant.intent","id":"intent-1","data":{"intent":"Checking the implementation"}}`, protocol.EventTypeReasoningSummary},
		{`{"type":"tool.execution_start","data":{"toolCallId":"tool-1","toolName":"powershell","arguments":{"command":"go test ./..."}}}`, protocol.EventTypeCommandStarted},
		{`{"type":"tool.execution_complete","data":{"toolCallId":"tool-1","success":true,"result":{"content":"ok"}}}`, protocol.EventTypeCommandCompleted},
		{`{"type":"assistant.usage","data":{"model":"gpt-5.4","inputTokens":120,"outputTokens":30,"copilotUsage":{"totalNanoAiu":125000000}}}`, protocol.EventTypeUsageReported},
		{`{"type":"session.idle","data":{}}`, protocol.EventTypeRunCompleted},
	}
	for _, test := range tests {
		parsed := ParseLine(test.line)
		if parsed.Malformed || len(parsed.Events) != 1 || parsed.Events[0].Type != test.eventType || !json.Valid(parsed.RawJSON) {
			t.Fatalf("ParseLine(%s) = %#v", test.line, parsed)
		}
		if test.eventType == protocol.EventTypeUsageReported {
			if parsed.Usage == nil || parsed.Usage.ActualModel == nil || *parsed.Usage.ActualModel != "gpt-5.4" || parsed.Usage.AICredits == nil || *parsed.Usage.AICredits != 0.125 {
				t.Fatalf("usage = %#v", parsed.Usage)
			}
			if parsed.Usage.InputTokens != nil || parsed.Usage.TotalTokens != nil {
				t.Fatalf("Copilot token usage must not be recorded: %#v", parsed.Usage)
			}
		}
	}
}

func TestParseCopilotCLIResultUsageAndMessageModel(t *testing.T) {
	message := ParseLine(`{"type":"assistant.message","id":"event-1","data":{"messageId":"message-1","model":"claude-haiku-4.5","content":"Done"}}`)
	if message.Usage == nil || message.Usage.ActualModel == nil || *message.Usage.ActualModel != "claude-haiku-4.5" {
		t.Fatalf("message usage = %#v", message.Usage)
	}
	if len(message.Events) != 1 || message.Events[0].Type != protocol.EventTypeAssistantMessage {
		t.Fatalf("message events = %#v", message.Events)
	}

	result := ParseLine(`{"type":"result","usage":{"premiumRequests":0.33}}`)
	if result.Usage == nil || result.Usage.AICredits == nil || *result.Usage.AICredits != 0.33 {
		t.Fatalf("result usage = %#v", result.Usage)
	}
	if len(result.Events) != 1 || result.Events[0].Type != protocol.EventTypeUsageReported {
		t.Fatalf("result events = %#v", result.Events)
	}
}

func TestParseCopilotThreadErrorAndMalformed(t *testing.T) {
	started := ParseLine(`{"type":"assistant.turn_start","sessionId":"session-123","data":{"turnId":"1"}}`)
	if started.ThreadID != "session-123" {
		t.Fatalf("thread ID = %q", started.ThreadID)
	}
	failed := ParseLine(`{"type":"session.error","data":{"errorType":"authentication","message":"Login required"}}`)
	if failed.Events[0].Type != protocol.EventTypeRunFailed {
		t.Fatalf("failed = %#v", failed)
	}
	malformed := ParseLine(`not-json`)
	if !malformed.Malformed || malformed.Events[0].Type != protocol.EventTypeError {
		t.Fatalf("malformed = %#v", malformed)
	}
	var malformedData map[string]string
	if err := json.Unmarshal(malformed.Events[0].Data, &malformedData); err != nil || malformedData["message"] != "not-json" {
		t.Fatalf("malformed message = %#v, err = %v", malformedData, err)
	}
	unknown := ParseLine(`{"type":"session.context_changed","data":{"cwd":"/repo"}}`)
	if !unknown.Ignored {
		t.Fatalf("unknown = %#v", unknown)
	}
}

func TestParseNonJSONLineShowsCleanedRawText(t *testing.T) {
	// The Copilot CLI's interactive renderer sometimes leaks through despite
	// --output-format json, wrapping plain narration in ANSI color codes.
	line := "\x1b[38;2;145;152;161m│ \x1b[39m\x1b[1mSearch \x1b[22m\x1b[38;2;145;152;161m(grep)\x1b[39m"
	parsed := ParseLine(line)
	if !parsed.Malformed || len(parsed.Events) != 1 || parsed.Events[0].Type != protocol.EventTypeError {
		t.Fatalf("parsed = %#v", parsed)
	}
	var data map[string]string
	if err := json.Unmarshal(parsed.Events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["message"] != "│ Search (grep)" {
		t.Fatalf("message = %q", data["message"])
	}
}

func TestParseBlankLineIsIgnored(t *testing.T) {
	for _, line := range []string{"", "   ", "\x1b[38;2;145;152;161m\x1b[39m"} {
		parsed := ParseLine(line)
		if !parsed.Ignored || len(parsed.Events) != 0 {
			t.Fatalf("ParseLine(%q) = %#v", line, parsed)
		}
	}
}

func TestIgnoreSubagentAssistantMessage(t *testing.T) {
	parsed := ParseLine(`{"type":"assistant.message","agentId":"subagent-1","data":{"messageId":"message-1","content":"internal"}}`)
	if !parsed.Ignored || len(parsed.Events) != 0 {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestDoesNotExposeCopilotExtendedThinking(t *testing.T) {
	parsed := ParseLine(`{"type":"assistant.reasoning","data":{"reasoningId":"reason-1","content":"private chain of thought"}}`)
	if !parsed.Ignored || len(parsed.Events) != 0 {
		t.Fatalf("parsed = %#v", parsed)
	}
}
