package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Config is the parsed view of one mcp.json file. The wire format mirrors
// Claude Code so users can copy-paste configs between the two:
//
//	{
//	  "mcpServers": {
//	    "filesystem": {
//	      "command": "npx",
//	      "args":    ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
//	      "env":     {"FOO": "bar"}
//	    },
//	    "remote-api": {
//	      "url":     "https://example.com/mcp",
//	      "headers": {"Authorization": "Bearer ..."}
//	    }
//	  }
//	}
//
// A server entry is classified as stdio if Command is non-empty, HTTP if
// URL is non-empty. Both set is an error.
type Config struct {
	Servers map[string]ServerEntry `json:"mcpServers"`
}

// ServerEntry is the union of stdio + HTTP shapes. The transport-specific
// fields are mutually exclusive — Validate enforces it.
type ServerEntry struct {
	// stdio fields
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// HTTP fields
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// Optional: disable a server without removing it from config.
	Disabled bool `json:"disabled,omitempty"`
}

// Kind reports which transport this entry uses. Returns "" when neither
// (caller treats it as an invalid entry and skips it).
func (e ServerEntry) Kind() string {
	if e.Command != "" {
		return "stdio"
	}
	if e.URL != "" {
		return "http"
	}
	return ""
}

// Validate enforces the union invariant — exactly one of Command/URL set —
// and surfaces a usable error message identifying the offender. Called by
// Load before returning so callers get pre-validated entries.
func (e ServerEntry) Validate(name string) error {
	if e.Command == "" && e.URL == "" {
		return fmt.Errorf("mcp server %q: must set either 'command' (stdio) or 'url' (http)", name)
	}
	if e.Command != "" && e.URL != "" {
		return fmt.Errorf("mcp server %q: cannot set both 'command' and 'url'", name)
	}
	return nil
}

// LoadConfig reads + merges the user-global config and the project-local
// config, returning the merged result.
//
//   - User-global lives at ~/.octo/mcp.json. Always loaded if present.
//   - Project-local lives at <projectDir>/.octo/mcp.json. Loaded when
//     projectDir is non-empty AND the file exists.
//
// Merge rule: project-local entries OVERRIDE user-global entries with the
// same name. This matches the skills layer's same-name precedence so
// users can keep a shared user-global config and tweak per-project.
//
// Disabled entries are filtered out at load time. Missing files are not an
// error — they produce a zero-server config.
func LoadConfig(projectDir string) (*Config, error) {
	merged := &Config{Servers: map[string]ServerEntry{}}

	// 1. User-global.
	if home, err := os.UserHomeDir(); err == nil {
		userPath := filepath.Join(home, ".octo", "mcp.json")
		if cfg, err := readConfigFile(userPath); err != nil {
			return nil, err
		} else if cfg != nil {
			for name, e := range cfg.Servers {
				merged.Servers[name] = e
			}
		}
	}

	// 2. Project-local (overrides user-global on name collision).
	if projectDir != "" {
		projPath := filepath.Join(projectDir, ".octo", "mcp.json")
		if cfg, err := readConfigFile(projPath); err != nil {
			return nil, err
		} else if cfg != nil {
			for name, e := range cfg.Servers {
				merged.Servers[name] = e
			}
		}
	}

	// 3. Filter disabled + validate the rest.
	for name, e := range merged.Servers {
		if e.Disabled {
			delete(merged.Servers, name)
			continue
		}
		if err := e.Validate(name); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

// ServerNames returns the configured server names in stable sorted order,
// useful for deterministic /mcp output.
func (c *Config) ServerNames() []string {
	names := make([]string, 0, len(c.Servers))
	for n := range c.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func readConfigFile(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mcp: read %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("mcp: parse %s: %w", path, err)
	}
	return &cfg, nil
}
