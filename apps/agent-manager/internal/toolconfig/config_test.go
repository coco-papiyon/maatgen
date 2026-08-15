package toolconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestLoadRelativeToExecutable(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"providers":[{"id":"codex","label":"Codex CLI","models":["model-a","model-a","model-b"]}]}`)
	if err := os.WriteFile(filepath.Join(configDir, "providers.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	config, path, err := Load(filepath.Join(dir, "agent-manager.exe"), DefaultRelativePath)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(configDir, "providers.json") || config.Providers[0].Label != "Codex CLI" || len(config.Providers[0].Models) != 2 {
		t.Fatalf("path = %q, config = %#v", path, config)
	}
}

func TestLoadUsesEmbeddedDefaultWhenFileIsMissing(t *testing.T) {
	config, _, err := Load(filepath.Join(t.TempDir(), "agent-manager"), DefaultRelativePath)
	if err != nil || len(config.Providers) != 2 || config.Providers[0].ID != "codex" || config.Providers[1].ID != "copilot" {
		t.Fatalf("config = %#v, err = %v", config, err)
	}
}

func TestLoadRejectsPathOutsideExecutableDirectory(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "agent-manager"), "../providers.json"); err == nil {
		t.Fatal("expected path validation error")
	}
}

func TestParseModelCatalog(t *testing.T) {
	models, err := parseModelCatalog([]byte(`{"models":[{"slug":"model-a"},{"model":"model-b"},{"id":"hidden","hidden":true},{"slug":"model-a"}]}`))
	if err != nil || len(models) != 2 || models[0] != "model-a" || models[1] != "model-b" {
		t.Fatalf("models = %#v, err = %v", models, err)
	}
}

func TestSaveDefaultModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "providers.json")
	config := Config{Providers: []protocol.Provider{{ID: protocol.AgentCodex, Label: "Codex", Models: []string{"model-a"}}}}
	if err := SaveDefaultModel(path, &config, protocol.AgentCodex, "model-b"); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(filepath.Join(dir, "agent-manager.exe"), DefaultRelativePath)
	if err != nil || loaded.Providers[0].DefaultModel != "model-b" || len(loaded.Providers[0].Models) != 2 {
		t.Fatalf("loaded = %#v, err = %v", loaded, err)
	}
}

func TestLoadFromUsesWorkingDirectoryForGoRunExecutable(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"providers":[{"id":"codex","label":"Codex","models":["model-a"]}]}`)
	if err := os.WriteFile(filepath.Join(configDir, "providers.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	config, path, err := LoadFrom(filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "agent-manager.exe"), DefaultRelativePath, dir)
	if err != nil || path != filepath.Join(configDir, "providers.json") || config.Providers[0].Models[0] != "model-a" {
		t.Fatalf("path = %q, config = %#v, err = %v", path, config, err)
	}
}
