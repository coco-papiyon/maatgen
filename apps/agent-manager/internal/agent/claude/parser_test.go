package claude

import (
	"encoding/json"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestParseSystemInitCarriesThreadAndModel(t *testing.T) {
	parsed := ParseLine(`{"type":"system","subtype":"init","session_id":"01J-abc","model":"claude-sonnet-5-20260101","cwd":"C:/repository"}`)
	if parsed.ThreadID != "01J-abc" {
		t.Fatalf("thread = %q", parsed.ThreadID)
	}
	if parsed.Usage == nil || parsed.Usage.ActualModel == nil || *parsed.Usage.ActualModel != "claude-sonnet-5-20260101" {
		t.Fatalf("usage = %#v", parsed.Usage)
	}
	if len(parsed.Events) != 1 || parsed.Events[0].Type != protocol.EventTypeRunStarted {
		t.Fatalf("events = %#v", parsed.Events)
	}
}

func TestParseAssistantSplitsContentBlocks(t *testing.T) {
	parsed := ParseLine(`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_1","role":"assistant","content":[` +
		`{"type":"thinking","thinking":"checking auth.ts"},` +
		`{"type":"text","text":"auth.tsを確認します"},` +
		`{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go test ./..."}}]}}`)
	if parsed.Malformed || parsed.Ignored {
		t.Fatalf("parsed = %#v", parsed)
	}
	types := eventTypes(parsed)
	want := []string{protocol.EventTypeReasoningSummary, protocol.EventTypeAssistantMessage, protocol.EventTypeCommandStarted}
	if len(types) != len(want) {
		t.Fatalf("types = %v", types)
	}
	for index, value := range want {
		if types[index] != value {
			t.Fatalf("types = %v, want %v", types, want)
		}
	}
	command := decodeData(t, parsed.Events[2].Data)
	if command["itemId"] != "toolu_1" || command["command"] != "Bash" || command["status"] != "in_progress" {
		t.Fatalf("command = %#v", command)
	}
	arguments, _ := command["arguments"].(map[string]any)
	if arguments["command"] != "go test ./..." {
		t.Fatalf("arguments = %#v", command["arguments"])
	}
}

func TestParseSubAgentTurnIsIgnored(t *testing.T) {
	parsed := ParseLine(`{"type":"assistant","session_id":"s1","parent_tool_use_id":"toolu_9","message":{"id":"msg_2","role":"assistant","content":[{"type":"text","text":"sub agent"}]}}`)
	if !parsed.Ignored || len(parsed.Events) != 0 {
		t.Fatalf("parsed = %#v", parsed)
	}
	if parsed.ThreadID != "s1" {
		t.Fatalf("thread = %q", parsed.ThreadID)
	}
}

func TestParseToolResultCompletesCommand(t *testing.T) {
	parsed := ParseLine(`{"type":"user","session_id":"s1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok","is_error":false}]}}`)
	if len(parsed.Events) != 1 || parsed.Events[0].Type != protocol.EventTypeCommandCompleted {
		t.Fatalf("events = %#v", parsed.Events)
	}
	data := decodeData(t, parsed.Events[0].Data)
	if data["itemId"] != "toolu_1" || data["status"] != "completed" || data["result"] != "ok" {
		t.Fatalf("data = %#v", data)
	}
	failed := ParseLine(`{"type":"user","session_id":"s1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"boom","is_error":true}]}}`)
	failedData := decodeData(t, failed.Events[0].Data)
	if failedData["status"] != "failed" || failedData["error"] != "boom" {
		t.Fatalf("failed data = %#v", failedData)
	}
}

func TestParseResultRecordsUsageAndReportedCost(t *testing.T) {
	parsed := ParseLine(`{"type":"result","subtype":"success","is_error":false,"session_id":"s1","result":"done",` +
		`"total_cost_usd":0.1234,"usage":{"input_tokens":10,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000,"output_tokens":40},` +
		`"modelUsage":{"claude-haiku-4-5":{"outputTokens":5},"claude-opus-5":{"outputTokens":35}}}`)
	usage := parsed.Usage
	if usage == nil {
		t.Fatal("usage is missing")
	}
	if usage.InputTokens == nil || *usage.InputTokens != 3210 {
		t.Fatalf("input = %v", usage.InputTokens)
	}
	if usage.CachedInputTokens == nil || *usage.CachedInputTokens != 3000 {
		t.Fatalf("cached = %v", usage.CachedInputTokens)
	}
	if usage.TotalTokens == nil || *usage.TotalTokens != 3250 {
		t.Fatalf("total = %v", usage.TotalTokens)
	}
	if usage.CostUSD == nil || *usage.CostUSD != 0.1234 {
		t.Fatalf("cost = %v", usage.CostUSD)
	}
	if usage.ActualModel == nil || *usage.ActualModel != "claude-opus-5" {
		t.Fatalf("actual model = %v", usage.ActualModel)
	}
	types := eventTypes(parsed)
	if len(types) != 2 || types[0] != protocol.EventTypeUsageReported || types[1] != protocol.EventTypeRunCompleted {
		t.Fatalf("types = %v", types)
	}
}

func TestParseResultFailureReportsRunFailed(t *testing.T) {
	parsed := ParseLine(`{"type":"result","subtype":"error_max_turns","is_error":true,"session_id":"s1","result":"turn limit reached","usage":{"input_tokens":1,"output_tokens":2}}`)
	types := eventTypes(parsed)
	if len(types) != 2 || types[1] != protocol.EventTypeRunFailed {
		t.Fatalf("types = %v", types)
	}
	data := decodeData(t, parsed.Events[1].Data)
	if data["code"] != "error_max_turns" || data["message"] != "turn limit reached" {
		t.Fatalf("data = %#v", data)
	}
}

func TestParseInvalidLineIsMalformed(t *testing.T) {
	parsed := ParseLine("not json")
	if !parsed.Malformed || len(parsed.Events) != 1 || parsed.Events[0].Type != protocol.EventTypeError {
		t.Fatalf("parsed = %#v", parsed)
	}
	data := decodeData(t, parsed.Events[0].Data)
	if data["code"] != "invalid_claude_jsonl" {
		t.Fatalf("data = %#v", data)
	}
}

func TestParseUnknownEventIsIgnored(t *testing.T) {
	parsed := ParseLine(`{"type":"stream_event","session_id":"s1","event":{"type":"content_block_delta"}}`)
	if !parsed.Ignored || len(parsed.Events) != 0 {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func eventTypes(parsed agent.ParsedLine) []string {
	types := make([]string, 0, len(parsed.Events))
	for _, event := range parsed.Events {
		types = append(types, event.Type)
	}
	return types
}

func decodeData(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	return data
}
