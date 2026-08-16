package security

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactJSONMasksKeysAndEmbeddedSecrets(t *testing.T) {
	raw := json.RawMessage(`{
		"api_key":"key-value",
		"nested":{"refresh_token":"refresh-value","message":"Authorization: Bearer abc.def and sk-1234567890"},
		"input_tokens":100
	}`)
	redacted, err := RedactJSON(raw)
	if err != nil {
		t.Fatalf("redact JSON: %v", err)
	}
	text := string(redacted)
	for _, secret := range []string{"key-value", "refresh-value", "abc.def", "sk-1234567890"} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret %q remains in %s", secret, text)
		}
	}
	if !strings.Contains(text, `"input_tokens":100`) {
		t.Fatalf("usage field was over-redacted: %s", text)
	}
}

func TestRedactJSONRejectsInvalidJSON(t *testing.T) {
	if _, err := RedactJSON(json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestRedactStringMasksCommandCredentials(t *testing.T) {
	redacted := RedactString("curl -H 'Authorization: Bearer secret-token' --api-key=sk-abcdefgh123 --password plain-secret")
	if strings.Contains(redacted, "secret-token") || strings.Contains(redacted, "sk-abcdefgh123") || strings.Contains(redacted, "plain-secret") {
		t.Fatalf("redacted command = %q", redacted)
	}
}
