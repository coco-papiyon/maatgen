package storage

import (
	"encoding/json"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type RedactedRawEvent struct {
	ID        int64              `json:"id"`
	SessionID string             `json:"sessionId"`
	RunID     *string            `json:"runId,omitempty"`
	Agent     protocol.AgentName `json:"agent"`
	RawJSON   json.RawMessage    `json:"rawJson"`
	CreatedAt time.Time          `json:"createdAt"`
}
