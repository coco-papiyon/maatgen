package pricing

import (
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestExtractPricing(t *testing.T) {
	rate, ok := extract("GPT-5.4 Pricing Input $2.50 Cached Input $0.25 Output $15.00", "gpt-5.4")
	if !ok || rate.InputPerMillion != 2.5 || rate.CachedInputPerMillion != 0.25 || rate.OutputPerMillion != 15 {
		t.Fatalf("rate = %#v, ok = %v", rate, ok)
	}
	rate, ok = extract("GPT-5.6 Sol Input $5.00 Cached Input $0.50 Cache write $6.25 Output $30.00", "gpt-5.6-sol")
	if !ok || rate.CacheWritePerMillion != 6.25 || rate.OutputPerMillion != 30 {
		t.Fatalf("rate with cache write = %#v, ok = %v", rate, ok)
	}
}

func TestCostUSDUsesUncachedInputAndCachedInputRates(t *testing.T) {
	input, cached, output := int64(1000), int64(200), int64(300)
	cost := CostUSD(protocol.TokenUsage{InputTokens: &input, CachedInputTokens: &cached, OutputTokens: &output}, ModelPricing{
		InputPerMillion: 2, CachedInputPerMillion: 0.2, OutputPerMillion: 10,
	}, nil)
	want := 800.0*2/1_000_000 + 200.0*0.2/1_000_000 + 300.0*10/1_000_000
	if cost != want { t.Fatalf("cost = %v, want %v", cost, want) }
}

func TestCopilotCreditsCost(t *testing.T) {
	credits := 2.5
	if got := CostUSD(protocol.TokenUsage{}, ModelPricing{}, &credits); got != 0.025 { t.Fatalf("cost = %v", got) }
}
