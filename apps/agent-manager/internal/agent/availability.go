package agent

import (
	"context"
	"sync"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// AvailableProviders checks every Adapter's CLI installation concurrently
// (each Check applies its own timeout) and reports which ones succeeded.
func AvailableProviders(ctx context.Context, adapters []Adapter) map[protocol.AgentName]bool {
	available := make(map[protocol.AgentName]bool, len(adapters))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, adapter := range adapters {
		wg.Add(1)
		go func(adapter Adapter) {
			defer wg.Done()
			_, err := adapter.Check(ctx)
			mu.Lock()
			available[adapter.Name()] = err == nil
			mu.Unlock()
		}(adapter)
	}
	wg.Wait()
	return available
}
