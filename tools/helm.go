package tools

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// helmRelease mirrors the Helm 3 release JSON structure (fields we care about).
type helmRelease struct {
	Name      string          `json:"name"`
	Namespace string          `json:"namespace"`
	Info      helmReleaseInfo `json:"info"`
	Chart     helmChart       `json:"chart"`
	Config    map[string]any  `json:"config"`
	Version   int             `json:"version"` // revision number
}

type helmReleaseInfo struct {
	Status        string `json:"status"`
	FirstDeployed string `json:"first_deployed"`
	LastDeployed  string `json:"last_deployed"`
	Description   string `json:"description"`
	Notes         string `json:"notes"`
}

type helmChart struct {
	Metadata helmChartMetadata `json:"metadata"`
}

type helmChartMetadata struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	AppVersion  string `json:"appVersion"`
	Description string `json:"description"`
}

// decodeHelmRelease decodes Helm 3 release data from a Secret's "release" field.
// Helm stores releases as: base64 → gzip → JSON.
// Kubernetes already base64-decodes Secret.Data values, so we start with the inner base64.
func decodeHelmRelease(data []byte) (*helmRelease, error) {
	// Step 1: base64 decode (Helm's own encoding on top of the Secret's base64)
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	// Step 2: gzip decompress
	gz, err := gzip.NewReader(bytes.NewReader(decoded))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	jsonBytes, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("gzip read: %w", err)
	}

	// Step 3: JSON unmarshal
	var rel helmRelease
	if err := json.Unmarshal(jsonBytes, &rel); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return &rel, nil
}

// findHelmReleaseSecret finds the latest-revision deployed Secret for a Helm release.
func findHelmReleaseSecret(ctx context.Context, clientset *kubernetes.Clientset, name, namespace string) (*helmRelease, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	selector := fmt.Sprintf("owner=helm,name=%s,status=deployed", name)
	secrets, err := clientset.CoreV1().Secrets(namespace).List(timeoutCtx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}

	if len(secrets.Items) == 0 {
		return nil, fmt.Errorf("no deployed Helm release %q found in namespace %q", name, namespace)
	}

	// Find the secret with the highest revision (version label)
	latest := &secrets.Items[0]
	for i := 1; i < len(secrets.Items); i++ {
		if secrets.Items[i].Labels["version"] > latest.Labels["version"] {
			latest = &secrets.Items[i]
		}
	}

	releaseData, ok := latest.Data["release"]
	if !ok {
		return nil, fmt.Errorf("secret %q missing 'release' data field", latest.Name)
	}

	return decodeHelmRelease(releaseData)
}
