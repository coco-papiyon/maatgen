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
	"slices"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

const DefaultRelativePath = "config/providers.json"

//go:embed default.json
var defaultConfig []byte

type Config struct {
	Providers       []protocol.Provider   `json:"providers"`
	CommandApproval CommandApprovalConfig `json:"commandApproval"`
}

type AllowedCommand struct {
	Argv []string `json:"argv"`
}

type ApprovalReviewerConfig struct {
	Provider        string            `json:"provider"`
	ModelByProvider map[string]string `json:"modelByProvider"`
	TimeoutSeconds  int               `json:"timeoutSeconds"`
	MinConfidence   float64           `json:"minConfidence"`
}

type CommandApprovalConfig struct {
	Enabled                     bool                   `json:"enabled"`
	AutoApproveMaxRisk          protocol.ApprovalRisk  `json:"autoApproveMaxRisk"`
	HumanResponseTimeoutSeconds int                    `json:"humanResponseTimeoutSeconds"`
	Reviewer                    ApprovalReviewerConfig `json:"reviewer"`
	AllowedCommands             []AllowedCommand       `json:"allowedCommands"`
}

func SaveAllowedCommand(path string, config *Config, argv []string) error {
	normalized := normalizeArgv(argv)
	if len(normalized) == 0 {
		return errors.New("allowed command argv is required")
	}
	for _, rule := range config.CommandApproval.AllowedCommands {
		if slices.Equal(rule.Argv, normalized) {
			return nil
		}
	}
	config.CommandApproval.AllowedCommands = append(config.CommandApproval.AllowedCommands, AllowedCommand{Argv: normalized})
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tool config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create tool config directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".providers-*.json")
	if err != nil {
		return fmt.Errorf("create temporary tool config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary tool config: %w", err)
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary tool config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary tool config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace tool config: %w", err)
	}
	return nil
}

func normalizeArgv(argv []string) []string {
	result := make([]string, 0, len(argv))
	for _, value := range argv {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func SaveDefaultModel(path string, config *Config, providerID protocol.AgentName, model string) error {
	model = strings.TrimSpace(model)
	for index := range config.Providers {
		provider := &config.Providers[index]
		if provider.ID != providerID {
			continue
		}
		provider.DefaultModel = model
		if model != "" && !contains(provider.Models, model) {
			provider.Models = append(provider.Models, model)
		}
		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return fmt.Errorf("encode tool config: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create tool config directory: %w", err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			return fmt.Errorf("write tool config: %w", err)
		}
		return nil
	}
	return fmt.Errorf("provider %q is not configured", providerID)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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

// Load resolves relativePath from the manager executable.
func Load(executablePath, relativePath string) (Config, string, error) {
	return LoadFrom(executablePath, relativePath, "")
}

// LoadFrom uses the working directory when the executable is a temporary go run binary.
func LoadFrom(executablePath, relativePath, workingDirectory string) (Config, string, error) {
	if filepath.IsAbs(relativePath) {
		return Config{}, "", errors.New("tool config path must be relative to the executable")
	}
	base := filepath.Dir(executablePath)
	if workingDirectory != "" && isGoRunExecutable(executablePath) {
		base = workingDirectory
	}
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

func isGoRunExecutable(executablePath string) bool {
	executablePath = filepath.Clean(executablePath)
	tempDir := filepath.Clean(os.TempDir())
	if rel, err := filepath.Rel(tempDir, executablePath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(executablePath), "/") {
		if strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	return false
}

func parse(data []byte) (Config, error) {
	approvalEnabled := true
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err == nil {
		if raw, ok := fields["commandApproval"]; ok {
			var approvalFields map[string]json.RawMessage
			if json.Unmarshal(raw, &approvalFields) == nil {
				if enabled, ok := approvalFields["enabled"]; ok {
					_ = json.Unmarshal(enabled, &approvalEnabled)
				}
			}
		}
	}
	var config Config
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	config.CommandApproval.Enabled = approvalEnabled
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
		provider.DefaultModel = strings.TrimSpace(provider.DefaultModel)
		if provider.DefaultModel != "" && !contains(provider.Models, provider.DefaultModel) {
			return Config{}, fmt.Errorf("provider %q default model is not configured", provider.ID)
		}
	}
	if _, ok := seen[protocol.AgentCodex]; !ok {
		return Config{}, errors.New("codex provider is required")
	}
	approval := &config.CommandApproval
	if approval.AutoApproveMaxRisk == "" {
		approval.AutoApproveMaxRisk = protocol.ApprovalRiskLow
	}
	if approval.AutoApproveMaxRisk != protocol.ApprovalRiskSafe && approval.AutoApproveMaxRisk != protocol.ApprovalRiskLow && approval.AutoApproveMaxRisk != protocol.ApprovalRiskHigh {
		return Config{}, errors.New("commandApproval.autoApproveMaxRisk must be safe, low, or high")
	}
	if approval.HumanResponseTimeoutSeconds == 0 {
		approval.HumanResponseTimeoutSeconds = 600
	}
	if approval.HumanResponseTimeoutSeconds < 1 || approval.HumanResponseTimeoutSeconds > 86400 {
		return Config{}, errors.New("commandApproval.humanResponseTimeoutSeconds is out of range")
	}
	if approval.Reviewer.Provider == "" {
		approval.Reviewer.Provider = "same-as-run"
	}
	if approval.Reviewer.Provider != "same-as-run" {
		return Config{}, errors.New("commandApproval.reviewer.provider must be same-as-run")
	}
	if approval.Reviewer.TimeoutSeconds == 0 {
		approval.Reviewer.TimeoutSeconds = 15
	}
	if approval.Reviewer.TimeoutSeconds < 1 || approval.Reviewer.TimeoutSeconds > 300 {
		return Config{}, errors.New("commandApproval.reviewer.timeoutSeconds is out of range")
	}
	if approval.Reviewer.MinConfidence == 0 {
		approval.Reviewer.MinConfidence = 0.8
	}
	if approval.Reviewer.MinConfidence < 0 || approval.Reviewer.MinConfidence > 1 {
		return Config{}, errors.New("commandApproval.reviewer.minConfidence must be between 0 and 1")
	}
	for index := range approval.AllowedCommands {
		approval.AllowedCommands[index].Argv = normalizeArgv(approval.AllowedCommands[index].Argv)
		if len(approval.AllowedCommands[index].Argv) == 0 {
			return Config{}, errors.New("commandApproval.allowedCommands argv is required")
		}
	}
	for providerID, model := range approval.Reviewer.ModelByProvider {
		model = strings.TrimSpace(model)
		found := false
		for _, provider := range config.Providers {
			if string(provider.ID) == providerID && contains(provider.Models, model) {
				found = true
				break
			}
		}
		if model == "" || !found {
			return Config{}, fmt.Errorf("commandApproval reviewer model %q is not configured for provider %q", model, providerID)
		}
		approval.Reviewer.ModelByProvider[providerID] = model
	}
	return config, nil
}
