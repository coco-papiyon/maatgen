package providerusage

import (
	"context"
	"errors"
	"sort"

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

// GetAllProviderUsage fetches usage for every registered provider so a UI can
// show them side by side. A provider that fails to report usage (rate
// limited, CLI not signed in, transient error, ...) is silently omitted
// rather than failing the whole request or logging noise the caller can't
// act on.
func (s *Service) GetAllProviderUsage(ctx context.Context, directory string) []protocol.ProviderUsage {
	results := make([]protocol.ProviderUsage, 0, len(s.readers))
	for provider, reader := range s.readers {
		usage, err := reader.GetUsage(ctx, directory)
		if err != nil {
			continue
		}
		usage.Provider = provider
		results = append(results, usage)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Provider < results[j].Provider })
	return results
}
