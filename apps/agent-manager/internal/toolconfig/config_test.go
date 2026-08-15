package toolconfig

import (
	"os"
	"path/filepath"
	"testing"
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
	if err != nil || len(config.Providers) != 1 || config.Providers[0].ID != "codex" {
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
