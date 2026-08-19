package claude

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// envelope covers the `--output-format stream-json` line shapes emitted by
// Claude Code in print mode: `system`, `assistant`, `user`, and `result`.
type envelope struct {
	Type            string                `json:"type"`
	Subtype         string                `json:"subtype"`
	SessionID       string                `json:"session_id"`
	UUID            string                `json:"uuid"`
	Model           string                `json:"model"`
	ParentToolUseID *string               `json:"parent_tool_use_id"`
	Message         json.RawMessage       `json:"message"`
	IsError         bool                  `json:"is_error"`
	Result          json.RawMessage       `json:"result"`
	TotalCostUSD    *float64              `json:"total_cost_usd"`
	Usage           *tokenCounts          `json:"usage"`
	ModelUsage      map[string]modelUsage `json:"modelUsage"`
}

type message struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// tokenCounts mirrors the Anthropic usage block. `input_tokens` excludes both
// cache writes and cache reads, so the common input total is the sum of all
// three fields.
type tokenCounts struct {
	InputTokens              *int64 `json:"input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
}

type modelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

func ParseLine(line string) agent.ParsedLine {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return malformedLine(line, "Claude Code emitted an invalid JSONL line")
	}
	parsed := agent.ParsedLine{RawJSON: json.RawMessage(append([]byte(nil), trimmed...))}
	var source envelope
	if err := json.Unmarshal(parsed.RawJSON, &source); err != nil || source.Type == "" {
		return malformedJSON(parsed.RawJSON, "Claude Code JSONL event is missing a valid type")
	}
	parsed.ThreadID = source.SessionID

	// Sub-agent turns stay in redacted raw storage but never reach the main chat.
	if source.ParentToolUseID != nil && *source.ParentToolUseID != "" {
		parsed.Ignored = true
		return parsed
	}

	switch source.Type {
	case "system":
		if source.Subtype != "init" {
			parsed.Ignored = true
			break
		}
		if source.Model != "" {
			usage := protocol.TokenUsage{Source: "cli", ActualModel: stringPointer(source.Model)}
			parsed.Usage = &usage
		}
		parsed.Events = []agent.NormalizedEvent{newEvent(protocol.EventTypeRunStarted, map[string]any{
			"claudeType": "system.init",
		})}
	case "assistant":
		return parseAssistant(parsed, source)
	case "user":
		return parseUser(parsed, source)
	case "result":
		return parseResult(parsed, source)
	default:
		parsed.Ignored = true
	}
	return parsed
}

func parseAssistant(parsed agent.ParsedLine, source envelope) agent.ParsedLine {
	blocks, decoded, ok := decodeContent(source.Message)
	if !ok {
		return malformedJSON(parsed.RawJSON, "Claude Code assistant event has invalid message content")
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			parsed.Events = append(parsed.Events, newEvent(protocol.EventTypeAssistantMessage, map[string]any{
				"itemId": firstNonEmpty(decoded.ID, source.UUID), "text": block.Text,
			}))
		case "thinking":
			if strings.TrimSpace(block.Thinking) == "" {
				continue
			}
			parsed.Events = append(parsed.Events, newEvent(protocol.EventTypeReasoningSummary, map[string]any{
				"itemId": firstNonEmpty(decoded.ID, source.UUID), "text": block.Thinking,
			}))
		case "tool_use":
			parsed.Events = append(parsed.Events, newEvent(protocol.EventTypeCommandStarted, map[string]any{
				"itemId": block.ID, "command": block.Name, "arguments": rawOrEmptyObject(block.Input), "status": "in_progress",
			}))
		}
	}
	if len(parsed.Events) == 0 {
		parsed.Ignored = true
	}
	return parsed
}

func parseUser(parsed agent.ParsedLine, source envelope) agent.ParsedLine {
	// A user line carries tool results for the preceding assistant turn. Plain
	// string content is the prompt echo, which the Manager already recorded.
	blocks, _, ok := decodeContent(source.Message)
	if !ok {
		parsed.Ignored = true
		return parsed
	}
	for _, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		data := map[string]any{
			"itemId": block.ToolUseID, "status": completionStatus(!block.IsError),
			"result": rawOrEmptyObject(block.Content),
		}
		if block.IsError {
			data["error"] = rawOrEmptyObject(block.Content)
		}
		parsed.Events = append(parsed.Events, newEvent(protocol.EventTypeCommandCompleted, data))
	}
	if len(parsed.Events) == 0 {
		parsed.Ignored = true
	}
	return parsed
}

func parseResult(parsed agent.ParsedLine, source envelope) agent.ParsedLine {
	usage := protocol.TokenUsage{Source: "cli"}
	if source.Usage != nil {
		input := sumInt64(source.Usage.InputTokens, source.Usage.CacheCreationInputTokens, source.Usage.CacheReadInputTokens)
		usage.InputTokens = input
		usage.CachedInputTokens = source.Usage.CacheReadInputTokens
		usage.OutputTokens = source.Usage.OutputTokens
		usage.TotalTokens = sumInt64(input, source.Usage.OutputTokens)
	}
	if model := dominantModel(source.ModelUsage); model != "" {
		usage.ActualModel = stringPointer(model)
	}
	// Claude Code prices the turn itself, including per-model cache read and
	// cache write rates, so the reported amount is authoritative for the Run.
	if source.TotalCostUSD != nil {
		cost := *source.TotalCostUSD
		usage.CostUSD = &cost
	}
	parsed.Usage = &usage
	parsed.Events = append(parsed.Events, newEvent(protocol.EventTypeUsageReported, usage))

	if source.IsError || (source.Subtype != "" && source.Subtype != "success") {
		parsed.Events = append(parsed.Events, newEvent(protocol.EventTypeRunFailed, map[string]any{
			"code":    firstNonEmpty(source.Subtype, "claude_error"),
			"message": firstNonEmpty(resultText(source.Result), "Claude Code reported an unspecified error"),
		}))
		return parsed
	}
	parsed.Events = append(parsed.Events, newEvent(protocol.EventTypeRunCompleted, map[string]any{
		"claudeType": "result", "subtype": source.Subtype,
	}))
	return parsed
}

func decodeContent(raw json.RawMessage) ([]contentBlock, message, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, message{}, false
	}
	var decoded message
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, message{}, false
	}
	if len(decoded.Content) == 0 || string(decoded.Content) == "null" {
		return nil, decoded, false
	}
	var blocks []contentBlock
	if err := json.Unmarshal(decoded.Content, &blocks); err != nil {
		// Content is a plain string when the turn carries no structured blocks.
		var text string
		if json.Unmarshal(decoded.Content, &text) == nil {
			return []contentBlock{{Type: "text", Text: text}}, decoded, true
		}
		return nil, decoded, false
	}
	return blocks, decoded, true
}

// dominantModel picks the model that produced the most output tokens so that a
// Run using cheap sub-agent models still reports its main model. Keys are
// sorted first because Go map iteration order is not stable.
func dominantModel(usage map[string]modelUsage) string {
	names := make([]string, 0, len(usage))
	for name := range usage {
		names = append(names, name)
	}
	sort.Strings(names)
	best := ""
	var bestTokens int64 = -1
	for _, name := range names {
		if usage[name].OutputTokens > bestTokens {
			best = name
			bestTokens = usage[name].OutputTokens
		}
	}
	return best
}

func resultText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

func sumInt64(values ...*int64) *int64 {
	var total int64
	found := false
	for _, value := range values {
		if value == nil {
			continue
		}
		total += *value
		found = true
	}
	if !found {
		return nil
	}
	return &total
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
			"code": "invalid_claude_jsonl", "message": message,
		})},
		Malformed: true,
	}
}

func newEvent(eventType string, data any) agent.NormalizedEvent {
	encoded, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("marshal normalized Claude Code event: %v", err))
	}
	return agent.NormalizedEvent{Type: eventType, Data: encoded}
}
