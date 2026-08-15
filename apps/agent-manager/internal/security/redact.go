package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const redacted = "***"

var (
	bearerPattern     = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	secretPattern     = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
	assignmentPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|password|authorization)(\s*[:=]\s*)([^\s,;]+)`)
)

func RedactJSON(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON for redaction: %w", err)
	}
	encoded, err := json.Marshal(redactValue(value))
	if err != nil {
		return nil, fmt.Errorf("encode redacted JSON: %w", err)
	}
	return encoded, nil
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveKey(key) {
				typed[key] = redacted
			} else {
				typed[key] = redactValue(child)
			}
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = redactValue(child)
		}
		return typed
	case string:
		return redactString(typed)
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	return normalized == "authorization" || normalized == "apikey" ||
		strings.HasSuffix(normalized, "password") || strings.HasSuffix(normalized, "secret") ||
		strings.HasSuffix(normalized, "token")
}

func redactString(value string) string {
	value = bearerPattern.ReplaceAllString(value, "Bearer "+redacted)
	value = secretPattern.ReplaceAllString(value, redacted)
	return assignmentPattern.ReplaceAllString(value, `$1$2`+redacted)
}
