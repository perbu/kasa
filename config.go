package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/util/homedir"
)

// Config represents the application configuration.
type Config struct {
	Kubernetes struct {
		Kubeconfig string `yaml:"kubeconfig"`
		Context    string `yaml:"context"`
	} `yaml:"kubernetes"`
	Agent struct {
		Model             string `yaml:"model"`
		Name              string `yaml:"name"`
		MaxToolCalls      int    `yaml:"max_tool_calls"`
		ToolWarnThreshold int    `yaml:"tool_warn_threshold"`
	} `yaml:"agent"`
	Deployments struct {
		Directory string `yaml:"directory"`
		Remote    string `yaml:"remote"`
	} `yaml:"deployments"`
	Prompts struct {
		System string `yaml:"system"`
	} `yaml:"prompts"`
	Credentials struct {
		GoogleAPIKey string `yaml:"google_api_key"`
		JinaAPIKey   string `yaml:"jina_api_key"`
	} `yaml:"credentials"`
}

// configDir returns ~/.kasa, creating it if needed.
func configDir() (string, error) {
	home := homedir.HomeDir()
	if home == "" {
		return "", fmt.Errorf("could not determine home directory")
	}
	dir := filepath.Join(home, ".kasa")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating config directory: %w", err)
	}
	return dir, nil
}

// loadConfig loads configuration from ~/.kasa/config.yaml.
// If the file does not exist, returns a zero-value Config (all defaults apply).
func loadConfig() (*Config, error) {
	var cfg Config

	dir, err := configDir()
	if err != nil {
		// Can't determine home dir — use defaults
		applyDefaults(&cfg)
		return &cfg, nil
	}

	path := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			applyDefaults(&cfg)
			return &cfg, nil
		}
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// applyDefaults fills in zero-value fields with sensible defaults.
func applyDefaults(cfg *Config) {
	if cfg.Agent.Model == "" {
		cfg.Agent.Model = "gemini-3-flash-preview"
	}
	if cfg.Agent.Name == "" {
		cfg.Agent.Name = "kasa"
	}
	if cfg.Agent.MaxToolCalls == 0 {
		cfg.Agent.MaxToolCalls = 25
	}
	if cfg.Agent.ToolWarnThreshold == 0 {
		cfg.Agent.ToolWarnThreshold = 3
	}
	if cfg.Deployments.Directory == "" {
		cfg.Deployments.Directory = "~/.kasa/deployments"
	}
	if cfg.Prompts.System == "" {
		cfg.Prompts.System = defaultSystemPrompt
	}
}

// GoogleAPIKey returns the Google API key, preferring the environment variable.
func (c *Config) GoogleAPIKey() string {
	if v := os.Getenv("GOOGLE_API_KEY"); v != "" {
		return v
	}
	return c.Credentials.GoogleAPIKey
}

// JinaAPIKey returns the Jina API key, preferring the environment variable.
func (c *Config) JinaAPIKey() string {
	if v := os.Getenv("JINA_API_KEY"); v != "" {
		return v
	}
	return c.Credentials.JinaAPIKey
}

