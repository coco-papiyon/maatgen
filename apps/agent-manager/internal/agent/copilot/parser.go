package copilot

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type envelope struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	AgentID   string          `json:"agentId"`
	SessionID string          `json:"sessionId"`
	Model     string          `json:"model"`
	Usage     *resultUsage    `json:"usage"`
	Data      json.RawMessage `json:"data"`
}

type eventData struct {
	SessionID       string          `json:"sessionId"`
	MessageID       string          `json:"messageId"`
	Content         string          `json:"content"`
	Intent          string          `json:"intent"`
	ToolCallID      string          `json:"toolCallId"`
	ToolName        string          `json:"toolName"`
	Arguments       json.RawMessage `json:"arguments"`
	Success         bool            `json:"success"`
	Result          json.RawMessage `json:"result"`
	Error           json.RawMessage `json:"error"`
	ErrorType       string          `json:"errorType"`
	Message         string          `json:"message"`
	ShutdownType    string          `json:"shutdownType"`
	InputTokens     *int64          `json:"inputTokens"`
	OutputTokens    *int64          `json:"outputTokens"`
	ReasoningTokens *int64          `json:"reasoningTokens"`
	CacheReadTokens *int64          `json:"cacheReadTokens"`
	Model           string          `json:"model"`
	CopilotUsage    *copilotUsage   `json:"copilotUsage"`
}

type copilotUsage struct {
	TotalNanoAIU *float64 `json:"totalNanoAiu"`
}

// resultUsage is emitted by the Copilot CLI's programmatic JSONL mode. Older
// CLI versions expose the billing unit as premiumRequests on the final result
// instead of emitting assistant.usage events.
type resultUsage struct {
	PremiumRequests *float64 `json:"premiumRequests"`
}

func ParseLine(line string) agent.ParsedLine {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return malformedLine(line, "GitHub Copilot emitted an invalid JSONL line")
	}
	parsed := agent.ParsedLine{RawJSON: json.RawMessage(append([]byte(nil), trimmed...))}
	var source envelope
	if err := json.Unmarshal(parsed.RawJSON, &source); err != nil || source.Type == "" {
		return malformedJSON(parsed.RawJSON, "GitHub Copilot JSONL event is missing a valid type")
	}
	var data eventData
	if len(source.Data) > 0 && string(source.Data) != "null" {
		if err := json.Unmarshal(source.Data, &data); err != nil {
			return malformedJSON(parsed.RawJSON, "GitHub Copilot JSONL event has invalid data")
		}
	}
	parsed.ThreadID = firstNonEmpty(source.SessionID, data.SessionID)

	// Sub-agent messages remain in raw storage but do not pollute the main chat.
	if source.AgentID != "" && strings.HasPrefix(source.Type, "assistant.") {
		parsed.Ignored = true
		return parsed
	}

	switch source.Type {
	case "assistant.turn_start":
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeRunStarted, map[string]any{"copilotType": source.Type})}
	case "assistant.message":
		if data.Model != "" {
			usage := protocol.TokenUsage{Source: "cli", ActualModel: stringPointer(data.Model)}
			parsed.Usage = &usage
		}
		if strings.TrimSpace(data.Content) == "" {
			parsed.Ignored = true
			break
		}
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeAssistantMessage, map[string]any{
			"itemId": firstNonEmpty(data.MessageID, source.ID), "text": data.Content,
		})}
	case "assistant.intent":
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeReasoningSummary, map[string]any{
			"itemId": source.ID, "text": data.Intent,
		})}
	case "assistant.reasoning", "assistant.reasoning_delta":
		// Copilot labels this as extended thinking rather than a summary.
		// Keep the redacted raw event for diagnostics but do not expose it as
		// user-facing reasoning.
		parsed.Ignored = true
	case "assistant.usage":
		usage := protocol.TokenUsage{Source: "cli"}
		if data.Model != "" {
			usage.ActualModel = &data.Model
		}
		if data.CopilotUsage != nil && data.CopilotUsage.TotalNanoAIU != nil {
			credits := *data.CopilotUsage.TotalNanoAIU / 1_000_000_000
			usage.AICredits = &credits
		}
		parsed.Usage = &usage
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeUsageReported, usage)}
	case "result":
		if source.Usage == nil || source.Usage.PremiumRequests == nil {
			parsed.Ignored = true
			break
		}
		// The CLI currently reports premiumRequests in its result envelope. It
		// is the only usage value available in this output format, so retain it
		// as the recorded Copilot credit quantity for the existing usage schema.
		usage := protocol.TokenUsage{Source: "cli", AICredits: source.Usage.PremiumRequests}
		if source.Model != "" {
			usage.ActualModel = stringPointer(source.Model)
		}
		parsed.Usage = &usage
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeUsageReported, usage)}
	case "tool.execution_start":
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeCommandStarted, map[string]any{
			"itemId": data.ToolCallID, "command": data.ToolName, "arguments": rawOrEmptyObject(data.Arguments), "status": "in_progress",
		})}
	case "tool.execution_complete":
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeCommandCompleted, map[string]any{
			"itemId": data.ToolCallID, "status": completionStatus(data.Success), "result": rawOrEmptyObject(data.Result), "error": rawOrEmptyObject(data.Error),
		})}
	case "session.idle", "assistant.turn_end", "session.task_complete":
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeRunCompleted, map[string]any{"copilotType": source.Type})}
	case "session.error":
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeRunFailed, map[string]any{
			"code": firstNonEmpty(data.ErrorType, "copilot_error"), "message": firstNonEmpty(data.Message, "GitHub Copilot reported an unspecified error"),
		})}
	case "session.shutdown":
		if data.ShutdownType == "error" {
			parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeRunFailed, map[string]any{
				"code": "copilot_shutdown_error", "message": firstNonEmpty(data.Message, "GitHub Copilot session stopped with an error"),
			})}
		} else {
			parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeRunCompleted, map[string]any{"copilotType": source.Type})}
		}
	default:
		parsed.Ignored = true
	}
	return parsed
}

func completionStatus(success bool) string {
	if success {
		return "completed"
	}
	return "failed"
}

func rawOrEmptyObject(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || string(value) == "null" {
		return json.RawMessage(`{}`)
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stringPointer(value string) *string {
	return &value
}

func malformedLine(line, message string) agent.ParsedLine {
	raw, _ := json.Marshal(map[string]string{"unparsedLine": line})
	return malformedJSON(raw, message)
}

func malformedJSON(raw json.RawMessage, message string) agent.ParsedLine {
	return agent.ParsedLine{
		RawJSON: raw,
		Events: []agent.NormalizedEvent{newEvent(protocol.EventTypeError, map[string]any{
			"code": "invalid_copilot_jsonl", "message": message,
		})},
		Malformed: true,
	}
}

func newEvent(eventType string, data any) agent.NormalizedEvent {
	encoded, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("marshal normalized GitHub Copilot event: %v", err))
	}
	return agent.NormalizedEvent{Type: eventType, Data: encoded}
}
