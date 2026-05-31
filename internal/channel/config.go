// Package channel provides IM platform bridging for octo-agent.
package channel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigDir is the user-level config directory.
const ConfigDir = ".octo"

// ConfigFile is the channel credentials file.
const ConfigFile = "channels.yml"

// Config manages IM platform credentials (Feishu, WeCom, etc.).
// Stored in ~/.octo/channels.yml.
type Config struct {
	Channels map[string]PlatformConfig `yaml:"channels,omitempty"`
}

// PlatformConfig is the raw per-platform configuration from YAML.
type PlatformConfig map[string]any

// ConfigPath returns the absolute path to channels.yml.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ConfigDir, ConfigFile), nil
}

// LoadConfig reads ~/.octo/channels.yml. A missing file returns an empty
// Config rather than an error.
func LoadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("channel config: %w", err)
	}
	return &cfg, nil
}

// Save writes the config to ~/.octo/channels.yml with mode 0600.
func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// EnabledPlatforms returns the list of platforms with enabled == true.
func (c *Config) EnabledPlatforms() []string {
	var out []string
	for name, pc := range c.Channels {
		if isEnabled(pc) {
			out = append(out, name)
		}
	}
	return out
}

// Platform returns the raw config for a platform, or nil if not present.
func (c *Config) Platform(name string) PlatformConfig {
	return c.Channels[name]
}

// SetPlatform merges fields into a platform's config, creating it if needed.
// Automatically sets enabled: true unless explicitly provided.
func (c *Config) SetPlatform(name string, fields map[string]any) {
	if c.Channels == nil {
		c.Channels = make(map[string]PlatformConfig)
	}
	pc := c.Channels[name]
	if pc == nil {
		pc = make(PlatformConfig)
	}
	for k, v := range fields {
		pc[k] = v
	}
	if _, ok := pc["enabled"]; !ok {
		pc["enabled"] = true
	}
	c.Channels[name] = pc
}

// RemovePlatform deletes a platform entry entirely.
func (c *Config) RemovePlatform(name string) {
	delete(c.Channels, name)
}

func isEnabled(pc PlatformConfig) bool {
	if pc == nil {
		return false
	}
	v, ok := pc["enabled"]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true" || val == "yes" || val == "1"
	case int:
		return val != 0
	default:
		return false
	}
}
