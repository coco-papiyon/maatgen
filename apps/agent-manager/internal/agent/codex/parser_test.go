package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestParseSuccessfulCodexJSONLFixture(t *testing.T) {
	lines := parseFixture(t, "testdata/success.jsonl")
	if len(lines) != 8 {
		t.Fatalf("parsed lines = %d, want 8", len(lines))
	}
	if lines[0].ThreadID != "0199a213-81c0-7800-8aa1-bbab2a035a53" || len(lines[0].Events) != 0 {
		t.Fatalf("thread line = %#v", lines[0])
	}

	wantTypes := []string{
		protocol.EventTypeRunStarted,
		protocol.EventTypeCommandStarted,
		protocol.EventTypeCommandCompleted,
		protocol.EventTypeReasoningSummary,
		protocol.EventTypeFileChangeReported,
		protocol.EventTypeAssistantMessage,
		protocol.EventTypeUsageReported,
		protocol.EventTypeRunCompleted,
	}
	var gotTypes []string
	for _, line := range lines {
		if line.Malformed || line.Ignored || !json.Valid(line.RawJSON) {
			t.Fatalf("unexpected line state = %#v", line)
		}
		for _, event := range line.Events {
			gotTypes = append(gotTypes, event.Type)
			if !json.Valid(event.Data) {
				t.Fatalf("invalid normalized data for %s: %s", event.Type, event.Data)
			}
		}
	}
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("event types = %#v, want %#v", gotTypes, wantTypes)
	}
	for index := range wantTypes {
		if gotTypes[index] != wantTypes[index] {
			t.Fatalf("event types = %#v, want %#v", gotTypes, wantTypes)
		}
	}

	completed := lines[len(lines)-1]
	if completed.Usage == nil || completed.Usage.TotalTokens == nil || *completed.Usage.TotalTokens != 24885 {
		t.Fatalf("usage = %#v", completed.Usage)
	}
	if completed.Usage.Source != "cli" {
		t.Fatalf("usage source = %q", completed.Usage.Source)
	}
}

func TestParseCommandAndFileChangePayloads(t *testing.T) {
	lines := parseFixture(t, "testdata/success.jsonl")
	var command struct {
		Command          string `json:"command"`
		AggregatedOutput string `json:"aggregatedOutput"`
		ExitCode         *int   `json:"exitCode"`
	}
	if err := json.Unmarshal(lines[3].Events[0].Data, &command); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	if command.Command != "go test ./..." || command.ExitCode == nil || *command.ExitCode != 0 || command.AggregatedOutput == "" {
		t.Fatalf("command payload = %#v", command)
	}

	var fileChange struct {
		Changes []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(lines[5].Events[0].Data, &fileChange); err != nil {
		t.Fatalf("decode file change: %v", err)
	}
	if len(fileChange.Changes) != 1 || fileChange.Changes[0].Path != "main.go" || fileChange.Changes[0].Kind != "update" {
		t.Fatalf("file change payload = %#v", fileChange)
	}
}

func TestParseCodexUsageActualModel(t *testing.T) {
	parsed := ParseLine(`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2,"model":"gpt-5.6"}}`)
	if parsed.Usage == nil || parsed.Usage.ActualModel == nil || *parsed.Usage.ActualModel != "gpt-5.6" {
		t.Fatalf("usage = %#v", parsed.Usage)
	}
}

func TestParseFailuresUnknownAndMalformedLines(t *testing.T) {
	lines := parseFixture(t, "testdata/failures.jsonl")
	if len(lines) != 4 {
		t.Fatalf("parsed lines = %d, want 4", len(lines))
	}
	if lines[0].Events[0].Type != protocol.EventTypeRunFailed {
		t.Fatalf("turn failure = %#v", lines[0])
	}
	if lines[1].Events[0].Type != protocol.EventTypeError {
		t.Fatalf("error event = %#v", lines[1])
	}
	if !lines[2].Ignored || lines[2].Malformed || len(lines[2].Events) != 0 {
		t.Fatalf("unknown event = %#v", lines[2])
	}
	if !lines[3].Malformed || lines[3].Events[0].Type != protocol.EventTypeError || !json.Valid(lines[3].RawJSON) {
		t.Fatalf("malformed line = %#v", lines[3])
	}
	var data struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(lines[3].Events[0].Data, &data); err != nil {
		t.Fatalf("decode malformed event: %v", err)
	}
	if data.Code != "invalid_codex_jsonl" || data.Message == "" {
		t.Fatalf("malformed data = %#v", data)
	}
}

func TestParseKnownIncompleteEventAsMalformed(t *testing.T) {
	parsed := ParseLine(`{"type":"thread.started"}`)
	if !parsed.Malformed || parsed.Ignored || len(parsed.Events) != 1 {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseAppServerNotifications(t *testing.T) {
	thread := ParseLine(`{"method":"thread/started","params":{"thread":{"id":"thread-app-server"}}}`)
	if thread.ThreadID != "thread-app-server" {
		t.Fatalf("thread = %#v", thread)
	}
	command := ParseLine(`{"method":"item/completed","params":{"item":{"id":"item-1","type":"commandExecution","command":"go test ./...","status":"completed","aggregatedOutput":"ok","exitCode":0}}}`)
	if len(command.Events) != 1 || command.Events[0].Type != protocol.EventTypeCommandCompleted {
		t.Fatalf("command = %#v", command)
	}
	usage := ParseLine(`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"last":{"inputTokens":10,"cachedInputTokens":4,"outputTokens":3,"totalTokens":13,"model":"gpt-5.4"}}}}`)
	if usage.Usage == nil || usage.Usage.ActualModel == nil || *usage.Usage.ActualModel != "gpt-5.4" || usage.Usage.TotalTokens == nil || *usage.Usage.TotalTokens != 13 {
		t.Fatalf("usage = %#v", usage.Usage)
	}
}

func TestAppServerApprovalCommandUsesExactCommandAndWorkingDirectory(t *testing.T) {
	command, cwd, reason := appServerApprovalCommand(json.RawMessage(`{"command":"go test ./... && git status","cwd":"C:/workspace/subdir","reason":"run tests","commandActions":[{"command":"go test ./..."},{"command":"git status"}]}`))
	if command != "go test ./... && git status" || cwd != "C:/workspace/subdir" || reason != "run tests" {
		t.Fatalf("command = %q, cwd = %q, reason = %q", command, cwd, reason)
	}
}

func parseFixture(t *testing.T, path string) []ParsedLine {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer file.Close()
	var parsed []ParsedLine
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parsed = append(parsed, ParseLine(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	return parsed
}
