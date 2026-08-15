package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type sessionCursorPayload struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

func encodeSessionCursor(session protocol.AgentSession) (string, error) {
	payload, err := json.Marshal(sessionCursorPayload{CreatedAt: session.CreatedAt.UTC(), ID: session.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeSessionCursor(value string) (*protocol.SessionCursor, error) {
	if value == "" {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var cursor sessionCursorPayload
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return nil, err
	}
	if cursor.CreatedAt.IsZero() || strings.TrimSpace(cursor.ID) == "" {
		return nil, errors.New("cursor fields are required")
	}
	return &protocol.SessionCursor{CreatedAt: cursor.CreatedAt.UTC(), ID: cursor.ID}, nil
}
