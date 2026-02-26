package tools

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestEnsureManagedByLabel_NoExistingLabels(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name": "test",
			},
		},
	}

	ensureManagedByLabel(obj)

	labels := obj.GetLabels()
	if labels == nil {
		t.Fatal("expected labels to be set, got nil")
	}
	if got := labels[managedByLabel]; got != managedByValue {
		t.Errorf("expected %s=%s, got %q", managedByLabel, managedByValue, got)
	}
}

func TestEnsureManagedByLabel_PreservesExistingLabels(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name": "test",
				"labels": map[string]any{
					"app.kubernetes.io/name": "myapp",
					"team":                   "platform",
				},
			},
		},
	}

	ensureManagedByLabel(obj)

	labels := obj.GetLabels()
	if got := labels["app.kubernetes.io/name"]; got != "myapp" {
		t.Errorf("existing label clobbered: app.kubernetes.io/name=%q", got)
	}
	if got := labels["team"]; got != "platform" {
		t.Errorf("existing label clobbered: team=%q", got)
	}
	if got := labels[managedByLabel]; got != managedByValue {
		t.Errorf("expected %s=%s, got %q", managedByLabel, managedByValue, got)
	}
}

func TestEnsureManagedByLabel_OverwritesWrongValue(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name": "test",
				"labels": map[string]any{
					managedByLabel: "helm",
				},
			},
		},
	}

	ensureManagedByLabel(obj)

	if got := obj.GetLabels()[managedByLabel]; got != managedByValue {
		t.Errorf("expected label to be overwritten to %s, got %q", managedByValue, got)
	}
}
