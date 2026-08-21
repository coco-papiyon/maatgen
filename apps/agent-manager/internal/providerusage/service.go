package providerusage

import (
	"context"
	"errors"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

var ErrUnavailable = errors.New("provider usage is unavailable")

type Service struct {
	readers map[protocol.AgentName]agent.UsageReader
}

func New(adapters []agent.Adapter) *Service {
	readers := make(map[protocol.AgentName]agent.UsageReader)
	for _, adapter := range adapters {
		if reader, ok := adapter.(agent.UsageReader); ok {
			readers[adapter.Name()] = reader
		}
	}
	return &Service{readers: readers}
}

func (s *Service) GetProviderUsage(ctx context.Context, provider protocol.AgentName, directory string) (protocol.ProviderUsage, error) {
	reader := s.readers[provider]
	if reader == nil {
		return protocol.ProviderUsage{}, ErrUnavailable
	}
	return reader.GetUsage(ctx, directory)
}
