package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type fakeAdapter struct {
	name     protocol.AgentName
	checkErr error
}

func (f fakeAdapter) Name() protocol.AgentName { return f.name }
func (f fakeAdapter) Check(context.Context) (Info, error) {
	if f.checkErr != nil {
		return Info{}, f.checkErr
	}
	return Info{Name: f.name}, nil
}
func (fakeAdapter) Run(context.Context, RunRequest, Emitter) (RunResult, error) {
	return RunResult{}, nil
}
func (fakeAdapter) ParseLine(string) ParsedLine { return ParsedLine{} }

func TestAvailableProviders(t *testing.T) {
	adapters := []Adapter{
		fakeAdapter{name: protocol.AgentCodex},
		fakeAdapter{name: protocol.AgentClaude, checkErr: errors.New("executable was not found")},
		fakeAdapter{name: protocol.AgentCopilot},
	}
	available := AvailableProviders(context.Background(), adapters)
	if !available[protocol.AgentCodex] || available[protocol.AgentClaude] || !available[protocol.AgentCopilot] {
		t.Fatalf("available = %#v", available)
	}
}
