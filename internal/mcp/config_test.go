package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadConfig_MissingFilesIsZero(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("expected empty config, got %d servers", len(cfg.Servers))
	}
}

func TestLoadConfig_UserGlobalOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeFile(t, filepath.Join(tmp, ".octo", "mcp.json"), `{
        "mcpServers": {
          "fs": {"command": "npx", "args": ["-y", "server-filesystem"]}
        }
    }`)
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Servers["fs"].Command; got != "npx" {
		t.Errorf("fs.Command = %q", got)
	}
	if got := cfg.Servers["fs"].Kind(); got != "stdio" {
		t.Errorf("fs.Kind() = %q, want stdio", got)
	}
}

func TestLoadConfig_ProjectOverridesUserGlobal(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeFile(t, filepath.Join(tmp, ".octo", "mcp.json"), `{
        "mcpServers": {
          "fs": {"command": "global-fs"}
        }
    }`)
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".octo", "mcp.json"), `{
        "mcpServers": {
          "fs":     {"command": "project-fs"},
          "github": {"url": "https://gh.example/mcp"}
        }
    }`)
	cfg, err := LoadConfig(proj)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// "fs" overridden by project entry.
	if got := cfg.Servers["fs"].Command; got != "project-fs" {
		t.Errorf("expected project override, got %q", got)
	}
	// "github" comes from project only.
	if got := cfg.Servers["github"].URL; got != "https://gh.example/mcp" {
		t.Errorf("github.URL = %q", got)
	}
	if got := cfg.Servers["github"].Kind(); got != "http" {
		t.Errorf("github.Kind() = %q, want http", got)
	}
}

func TestLoadConfig_DisabledIsSkipped(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeFile(t, filepath.Join(tmp, ".octo", "mcp.json"), `{
        "mcpServers": {
          "off": {"command": "x", "disabled": true},
          "on":  {"command": "y"}
        }
    }`)
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if _, ok := cfg.Servers["off"]; ok {
		t.Error("disabled entry should be filtered out")
	}
	if _, ok := cfg.Servers["on"]; !ok {
		t.Error("non-disabled entry missing")
	}
}

func TestLoadConfig_ValidationRejectsBothTransports(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeFile(t, filepath.Join(tmp, ".octo", "mcp.json"), `{
        "mcpServers": {
          "bad": {"command": "x", "url": "https://example.com/mcp"}
        }
    }`)
	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected validation error for entry with both command and url")
	}
}

func TestLoadConfig_ValidationRejectsNeither(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeFile(t, filepath.Join(tmp, ".octo", "mcp.json"), `{
        "mcpServers": {
          "bad": {"env": {"X": "y"}}
        }
    }`)
	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected validation error for entry with neither command nor url")
	}
}

func TestConfig_ServerNames_Sorted(t *testing.T) {
	c := &Config{Servers: map[string]ServerEntry{
		"z": {Command: "a"},
		"a": {Command: "a"},
		"m": {Command: "a"},
	}}
	names := c.ServerNames()
	if names[0] != "a" || names[1] != "m" || names[2] != "z" {
		t.Errorf("expected sorted names, got %v", names)
	}
}
