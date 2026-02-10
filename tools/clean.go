package tools

import (
	"encoding/base64"
	"strings"
)

// cleanForImport removes runtime-specific fields from a Kubernetes resource
// to make it suitable for saving as a manifest file.
func cleanForImport(resource map[string]any) {
	// Clean metadata fields
	if metadata, ok := resource["metadata"].(map[string]any); ok {
		// Remove runtime-assigned fields
		delete(metadata, "uid")
		delete(metadata, "resourceVersion")
		delete(metadata, "generation")
		delete(metadata, "creationTimestamp")
		delete(metadata, "managedFields")
		delete(metadata, "selfLink")

		// Clean annotations
		if annotations, ok := metadata["annotations"].(map[string]any); ok {
			keysToDelete := []string{}
			for key := range annotations {
				if shouldRemoveAnnotation(key) {
					keysToDelete = append(keysToDelete, key)
				}
			}
			for _, key := range keysToDelete {
				delete(annotations, key)
			}
			// Remove empty annotations map
			if len(annotations) == 0 {
				delete(metadata, "annotations")
			}
		}
	}

	// Remove entire status section
	delete(resource, "status")

	// Normalize Secret stringData → data (Kubernetes converts stringData to
	// base64-encoded data and drops stringData from the live object).
	if kind, _ := resource["kind"].(string); strings.EqualFold(kind, "Secret") {
		if stringData, ok := resource["stringData"].(map[string]any); ok {
			data, _ := resource["data"].(map[string]any)
			if data == nil {
				data = make(map[string]any)
			}
			for k, v := range stringData {
				if s, ok := v.(string); ok {
					data[k] = base64.StdEncoding.EncodeToString([]byte(s))
				}
			}
			resource["data"] = data
			delete(resource, "stringData")
		}
	}

	// Clean service-specific fields
	if spec, ok := resource["spec"].(map[string]any); ok {
		// clusterIP and clusterIPs are assigned by the cluster
		delete(spec, "clusterIP")
		delete(spec, "clusterIPs")
	}
}

// shouldRemoveAnnotation returns true if the annotation should be removed during import.
func shouldRemoveAnnotation(key string) bool {
	// Remove kubectl-managed annotations
	if strings.HasPrefix(key, "kubectl.kubernetes.io/") {
		return true
	}
	// Remove deployment revision annotations
	if strings.HasPrefix(key, "deployment.kubernetes.io/") {
		return true
	}
	// Remove change-cause annotation
	if key == "kubernetes.io/change-cause" {
		return true
	}
	return false
}
