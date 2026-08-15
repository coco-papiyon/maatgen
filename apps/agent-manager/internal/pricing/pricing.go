package pricing

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

const (
	OpenAISource     = "https://developers.openai.com/api/docs/models/compare"
	CopilotSource    = "https://docs.github.com/en/copilot/reference/copilot-billing/models-and-pricing"
	CopilotCreditUSD = 0.01
)

type ModelPricing struct {
	Provider              string
	Model                 string
	InputPerMillion       float64
	CachedInputPerMillion float64
	CacheWritePerMillion  float64
	OutputPerMillion      float64
	SourceURL             string
	RetrievedAt           time.Time
}

type Fetcher struct{ Client *http.Client }

func (f Fetcher) Refresh(ctx context.Context, models map[string][]string) []ModelPricing {
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	result := make([]ModelPricing, 0)
	for provider, names := range models {
		url := OpenAISource
		if provider == "copilot" {
			url = CopilotSource
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		body := make([]byte, 0)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := response.Body.Read(buf)
			body = append(body, buf[:n]...)
			if readErr != nil {
				break
			}
		}
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			continue
		}
		text := normalize(string(body))
		for _, name := range names {
			if rate, ok := extract(text, name); ok {
				rate.Provider = provider
				rate.Model = name
				rate.SourceURL = url
				rate.RetrievedAt = time.Now().UTC()
				result = append(result, rate)
			}
		}
	}
	return result
}

var moneyPattern = regexp.MustCompile(`\$([0-9]+(?:\.[0-9]+)?)`)

func normalize(value string) string {
	value = html.UnescapeString(value)
	value = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func extract(text, model string) (ModelPricing, bool) {
	parts := strings.Fields(strings.ReplaceAll(strings.ToUpper(model), "-", " "))
	pattern := ""
	for index, part := range parts {
		if index > 0 { pattern += `[ -]+` }
		pattern += regexp.QuoteMeta(part)
	}
	loc := regexp.MustCompile(`(?i)` + pattern).FindStringIndex(text)
	if loc == nil {
		return ModelPricing{}, false
	}
	end := loc[1] + 700
	if end > len(text) {
		end = len(text)
	}
	values := moneyPattern.FindAllStringSubmatch(text[loc[1]:end], -1)
	if len(values) < 3 {
		return ModelPricing{}, false
	}
	var numbers [4]float64
	for i := 0; i < len(values) && i < len(numbers); i++ {
		var parsed float64
		_, err := fmt.Sscanf(values[i][1], "%f", &parsed)
		if err != nil {
			return ModelPricing{}, false
		}
		numbers[i] = parsed
	}
	if len(values) == 3 {
		numbers[3] = numbers[2]
		numbers[2] = 0
	}
	return ModelPricing{InputPerMillion: numbers[0], CachedInputPerMillion: numbers[1], CacheWritePerMillion: numbers[2], OutputPerMillion: numbers[3]}, true
}

func CostUSD(usageTokens protocol.TokenUsage, pricing ModelPricing, credits *float64) float64 {
	if credits != nil {
		return *credits * CopilotCreditUSD
	}
	value := 0.0
	if usageTokens.InputTokens != nil {
		input := *usageTokens.InputTokens
		cached := int64(0)
		if usageTokens.CachedInputTokens != nil { cached = *usageTokens.CachedInputTokens }
		if cached > input { cached = input }
		value += float64(input-cached) * pricing.InputPerMillion / 1_000_000
	}
	if usageTokens.CachedInputTokens != nil {
		value += float64(*usageTokens.CachedInputTokens) * pricing.CachedInputPerMillion / 1_000_000
	}
	if usageTokens.OutputTokens != nil {
		value += float64(*usageTokens.OutputTokens) * pricing.OutputPerMillion / 1_000_000
	}
	return value
}
