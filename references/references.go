// Package references provides embedded Kubernetes resource documentation
// for the deployment agent to consult when generating manifests.
package references

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

//go:embed data/*.md
var content embed.FS

// Available topics (without .md extension)
var topics = []string{
	"deployment",
	"service",
	"secret",
	"configmap",
	"httproute",
	"gateway",
	"certificate",
	"clusterissuer",
	"postgres-cluster",
	"postgres-database",
}

// Lookup retrieves reference documentation for a given topic.
// Topic names are case-insensitive and the .md extension is optional.
func Lookup(topic string) (string, error) {
	// Normalize topic name
	topic = strings.ToLower(strings.TrimSuffix(topic, ".md"))

	data, err := content.ReadFile("data/" + topic + ".md")
	if err != nil {
		return "", fmt.Errorf("reference not found: %s (available: %s)", topic, strings.Join(topics, ", "))
	}
	return string(data), nil
}

// List returns all available reference topics.
func List() []string {
	return topics
}

// ListWithDescriptions returns topics with brief descriptions.
func ListWithDescriptions() map[string]string {
	return map[string]string{
		"deployment":        "Kubernetes Deployment - manages replicated pods",
		"service":           "Kubernetes Service - stable network endpoint for pods",
		"secret":            "Kubernetes Secret - stores sensitive data",
		"configmap":         "Kubernetes ConfigMap - stores configuration data",
		"httproute":         "Gateway API HTTPRoute - HTTP routing rules",
		"gateway":           "Gateway API Gateway - traffic entry point",
		"certificate":       "cert-manager Certificate - requests TLS certificates",
		"clusterissuer":     "cert-manager ClusterIssuer - cluster-wide certificate issuer",
		"postgres-cluster":  "CloudNativePG Cluster - PostgreSQL cluster management",
		"postgres-database": "CloudNativePG Database - declarative database creation",
	}
}

// MustLookup retrieves reference documentation, panicking on error.
// Useful for tests and initialization.
func MustLookup(topic string) string {
	ref, err := Lookup(topic)
	if err != nil {
		panic(err)
	}
	return ref
}

// All returns all reference documents concatenated.
// Useful for including everything in a system prompt (not recommended for large sets).
func All() (string, error) {
	var builder strings.Builder
	for _, topic := range topics {
		data, err := Lookup(topic)
		if err != nil {
			return "", err
		}
		builder.WriteString("# Reference: ")
		builder.WriteString(topic)
		builder.WriteString("\n\n")
		builder.WriteString(data)
		builder.WriteString("\n\n---\n\n")
	}
	return builder.String(), nil
}

// Walk iterates over all embedded reference files.
func Walk(fn func(topic string, content []byte) error) error {
	return fs.WalkDir(content, "data", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		data, err := content.ReadFile(path)
		if err != nil {
			return err
		}

		topic := strings.TrimSuffix(filepath.Base(path), ".md")
		return fn(topic, data)
	})
}
