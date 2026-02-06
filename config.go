package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration.
type Config struct {
	Kubernetes struct {
		Kubeconfig string `yaml:"kubeconfig"`
		Context    string `yaml:"context"`
	} `yaml:"kubernetes"`
	Agent struct {
		Model string `yaml:"model"`
		Name  string `yaml:"name"`
	} `yaml:"agent"`
	Deployments struct {
		Directory string `yaml:"directory"`
		Remote    string `yaml:"remote"`
	} `yaml:"deployments"`
	Prompts struct {
		System string `yaml:"system"`
	} `yaml:"prompts"`
}

// loadConfig loads the configuration from a YAML file.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return &cfg, nil
}
