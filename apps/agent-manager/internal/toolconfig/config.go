package toolconfig

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

const DefaultRelativePath = "config/providers.json"

//go:embed default.json
var defaultConfig []byte

type Config struct {
	Providers []protocol.Provider `json:"providers"`
}

// DiscoverCodexModels asks recent Codex binaries for their account-aware model
// catalog. The command is experimental, so callers must retain configured models
// as a fallback when this returns an error.
func DiscoverCodexModels(ctx context.Context, binaryName string) ([]string, error) {
	path, err := exec.LookPath(binaryName)
	if err != nil {
		return nil, err
	}
	output, err := exec.CommandContext(ctx, path, "debug", "models").Output()
	if err != nil {
		return nil, err
	}
	return parseModelCatalog(output)
}

func ApplyCodexModels(config Config, models []string) Config {
	if len(models) == 0 {
		return config
	}
	for index := range config.Providers {
		if config.Providers[index].ID == protocol.AgentCodex {
			config.Providers[index].Models = append([]string(nil), models...)
		}
	}
	return config
}

func parseModelCatalog(data []byte) ([]string, error) {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	var entries []any
	switch value := root.(type) {
	case []any:
		entries = value
	case map[string]any:
		for _, key := range []string{"models", "data"} {
			if list, ok := value[key].([]any); ok {
				entries = list
				break
			}
		}
	}
	models := make([]string, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		hidden, _ := item["hidden"].(bool)
		visibility, _ := item["visibility"].(string)
		if !ok || hidden || visibility == "hide" {
			continue
		}
		model := ""
		for _, key := range []string{"model", "slug", "id"} {
			if value, ok := item[key].(string); ok && strings.TrimSpace(value) != "" {
				model = strings.TrimSpace(value)
				break
			}
		}
		if model != "" {
			if _, exists := seen[model]; !exists {
				models = append(models, model)
				seen[model] = struct{}{}
			}
		}
	}
	if len(models) == 0 {
		return nil, errors.New("Codex model catalog did not contain visible models")
	}
	return models, nil
}

// Load resolves relativePath from the manager executable, never from its working directory.
func Load(executablePath, relativePath string) (Config, string, error) {
	if filepath.IsAbs(relativePath) {
		return Config{}, "", errors.New("tool config path must be relative to the executable")
	}
	base := filepath.Dir(executablePath)
	path := filepath.Clean(filepath.Join(base, relativePath))
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Config{}, "", errors.New("tool config path must stay under the executable directory")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = defaultConfig
	} else if err != nil {
		return Config{}, path, fmt.Errorf("read tool config: %w", err)
	}
	config, err := parse(data)
	if err != nil {
		return Config{}, path, fmt.Errorf("load tool config %s: %w", path, err)
	}
	return config, path, nil
}

func parse(data []byte) (Config, error) {
	var config Config
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	seen := make(map[protocol.AgentName]struct{}, len(config.Providers))
	for index := range config.Providers {
		provider := &config.Providers[index]
		provider.ID = protocol.AgentName(strings.TrimSpace(string(provider.ID)))
		provider.Label = strings.TrimSpace(provider.Label)
		if provider.ID == "" || provider.Label == "" {
			return Config{}, errors.New("provider id and label are required")
		}
		if _, exists := seen[provider.ID]; exists {
			return Config{}, fmt.Errorf("provider %q is duplicated", provider.ID)
		}
		seen[provider.ID] = struct{}{}
		models := make([]string, 0, len(provider.Models))
		modelSeen := map[string]struct{}{}
		for _, value := range provider.Models {
			model := strings.TrimSpace(value)
			if model == "" {
				return Config{}, fmt.Errorf("provider %q contains an empty model", provider.ID)
			}
			if _, exists := modelSeen[model]; !exists {
				models = append(models, model)
				modelSeen[model] = struct{}{}
			}
		}
		provider.Models = models
	}
	if _, ok := seen[protocol.AgentCodex]; !ok {
		return Config{}, errors.New("codex provider is required")
	}
	return config, nil
}
