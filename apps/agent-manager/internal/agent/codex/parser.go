package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type NormalizedEvent = agent.NormalizedEvent
type ParsedLine = agent.ParsedLine

type rawEnvelope struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     json.RawMessage `json:"item"`
	Usage    json.RawMessage `json:"usage"`
	Error    json.RawMessage `json:"error"`
	Message  string          `json:"message"`
}

type rawItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Command          string          `json:"command"`
	Status           string          `json:"status"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Changes          json.RawMessage `json:"changes"`
}

type rawUsage struct {
	InputTokens           *int64 `json:"input_tokens"`
	CachedInputTokens     *int64 `json:"cached_input_tokens"`
	OutputTokens          *int64 `json:"output_tokens"`
	ReasoningOutputTokens *int64 `json:"reasoning_output_tokens"`
}

func ParseLine(line string) ParsedLine {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return malformedLine(line, "Codex emitted an invalid JSONL line")
	}
	parsed := ParsedLine{RawJSON: json.RawMessage(append([]byte(nil), trimmed...))}
	var envelope rawEnvelope
	if err := json.Unmarshal(parsed.RawJSON, &envelope); err != nil || envelope.Type == "" {
		return malformedJSON(parsed.RawJSON, "Codex JSONL event is missing a valid type")
	}
	switch envelope.Type {
	case "thread.started":
		if envelope.ThreadID == "" {
			return malformedJSON(parsed.RawJSON, "Codex thread.started event is missing thread_id")
		}
		parsed.ThreadID = envelope.ThreadID
	case "turn.started":
		parsed.Events = []NormalizedEvent{newEvent(protocol.EventTypeRunStarted, map[string]any{
			"codexType": envelope.Type,
		})}
	case "turn.completed":
		if len(envelope.Usage) > 0 && string(envelope.Usage) != "null" {
			usage, err := parseUsage(envelope.Usage)
			if err != nil {
				return malformedJSON(parsed.RawJSON, "Codex turn.completed event has invalid usage")
			}
			parsed.Usage = &usage
			parsed.Events = append(parsed.Events, newEvent(protocol.EventTypeUsageReported, usage))
		}
		parsed.Events = append(parsed.Events, newEvent(protocol.EventTypeRunCompleted, map[string]any{
			"codexType": envelope.Type,
		}))
	case "turn.failed":
		parsed.Events = []NormalizedEvent{newEvent(protocol.EventTypeRunFailed, map[string]any{
			"message": eventErrorMessage(envelope),
		})}
	case "error":
		parsed.Events = []NormalizedEvent{newEvent(protocol.EventTypeError, map[string]any{
			"message": eventErrorMessage(envelope),
			"code":    "codex_error",
		})}
	case "item.started", "item.updated", "item.completed":
		return parseItem(parsed, envelope)
	default:
		parsed.Ignored = true
	}
	return parsed
}

func parseItem(parsed ParsedLine, envelope rawEnvelope) ParsedLine {
	if len(envelope.Item) == 0 || string(envelope.Item) == "null" {
		return malformedJSON(parsed.RawJSON, "Codex item event is missing item")
	}
	var item rawItem
	if err := json.Unmarshal(envelope.Item, &item); err != nil || item.Type == "" {
		return malformedJSON(parsed.RawJSON, "Codex item event has an invalid item")
	}

	switch item.Type {
	case "agent_message":
		if envelope.Type == "item.completed" {
			parsed.Events = []NormalizedEvent{newEvent(protocol.EventTypeAssistantMessage, map[string]any{
				"itemId": item.ID,
				"text":   item.Text,
			})}
		} else {
			parsed.Ignored = true
		}
	case "reasoning":
		if envelope.Type == "item.completed" {
			parsed.Events = []NormalizedEvent{newEvent(protocol.EventTypeReasoningSummary, map[string]any{
				"itemId": item.ID,
				"text":   item.Text,
			})}
		} else {
			parsed.Ignored = true
		}
	case "command_execution":
		data := map[string]any{
			"itemId":  item.ID,
			"command": item.Command,
			"status":  item.Status,
		}
		switch envelope.Type {
		case "item.started":
			parsed.Events = []NormalizedEvent{newEvent(protocol.EventTypeCommandStarted, data)}
		case "item.completed":
			data["aggregatedOutput"] = item.AggregatedOutput
			data["exitCode"] = item.ExitCode
			parsed.Events = []NormalizedEvent{newEvent(protocol.EventTypeCommandCompleted, data)}
		default:
			parsed.Ignored = true
		}
	case "file_change":
		if envelope.Type == "item.completed" {
			changes := json.RawMessage(`[]`)
			if len(item.Changes) > 0 && string(item.Changes) != "null" {
				changes = item.Changes
			}
			parsed.Events = []NormalizedEvent{newEvent(protocol.EventTypeFileChangeReported, map[string]any{
				"itemId":  item.ID,
				"status":  item.Status,
				"changes": changes,
			})}
		} else {
			parsed.Ignored = true
		}
	default:
		parsed.Ignored = true
	}
	return parsed
}

func parseUsage(raw json.RawMessage) (protocol.TokenUsage, error) {
	var source rawUsage
	if err := json.Unmarshal(raw, &source); err != nil {
		return protocol.TokenUsage{}, err
	}
	usage := protocol.TokenUsage{
		InputTokens: source.InputTokens, CachedInputTokens: source.CachedInputTokens,
		OutputTokens: source.OutputTokens, ReasoningOutputTokens: source.ReasoningOutputTokens,
		Source: "cli",
	}
	if source.InputTokens != nil && source.OutputTokens != nil {
		total := *source.InputTokens + *source.OutputTokens
		usage.TotalTokens = &total
	}
	return usage, nil
}

func eventErrorMessage(envelope rawEnvelope) string {
	if envelope.Message != "" {
		return envelope.Message
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		var structured struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &structured) == nil && structured.Message != "" {
			return structured.Message
		}
		var text string
		if json.Unmarshal(envelope.Error, &text) == nil && text != "" {
			return text
		}
	}
	return "Codex reported an unspecified error"
}

func malformedLine(line, message string) ParsedLine {
	raw, _ := json.Marshal(map[string]string{"unparsedLine": line})
	return malformedJSON(raw, message)
}

func malformedJSON(raw json.RawMessage, message string) ParsedLine {
	return ParsedLine{
		RawJSON: raw,
		Events: []NormalizedEvent{newEvent(protocol.EventTypeError, map[string]any{
			"code":    "invalid_codex_jsonl",
			"message": message,
		})},
		Malformed: true,
	}
}

func newEvent(eventType string, data any) NormalizedEvent {
	encoded, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("marshal normalized Codex event: %v", err))
	}
	return NormalizedEvent{Type: eventType, Data: encoded}
}
