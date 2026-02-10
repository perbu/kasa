package tools

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestNormalizeKindName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Canonical names pass through
		{"pod", "pod"},
		{"deployment", "deployment"},
		{"service", "service"},
		{"configmap", "configmap"},
		// Aliases resolve
		{"deploy", "deployment"},
		{"svc", "service"},
		{"cm", "configmap"},
		{"ns", "namespace"},
		{"po", "pod"},
		{"sts", "statefulset"},
		{"ds", "daemonset"},
		{"rs", "replicaset"},
		{"ing", "ingress"},
		{"gw", "gateway"},
		{"hpa", "horizontalpodautoscaler"},
		{"pvc", "persistentvolumeclaim"},
		{"sa", "serviceaccount"},
		{"netpol", "networkpolicy"},
		{"cert", "certificate"},
		{"cr", "certificaterequest"},
		{"gc", "gatewayclass"},
		// Plurals resolve
		{"pods", "pod"},
		{"deployments", "deployment"},
		{"services", "service"},
		{"configmaps", "configmap"},
		{"secrets", "secret"},
		{"namespaces", "namespace"},
		{"ingresses", "ingress"},
		// Case insensitive
		{"Deployment", "deployment"},
		{"PODS", "pod"},
		{"Service", "service"},
		// Unknown kinds pass through lowercase
		{"mycustomresource", "mycustomresource"},
		{"SomeNewCRD", "somenewcrd"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeKindName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeKindName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestLookupGVR(t *testing.T) {
	tests := []struct {
		kind    string
		wantOK  bool
		wantGVR schema.GroupVersionResource
	}{
		{"deployment", true, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
		{"deploy", true, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
		{"pod", true, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}},
		{"service", true, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}},
		{"svc", true, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}},
		{"ingress", true, schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}},
		{"httproute", true, schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}},
		{"certificate", true, schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}},
		{"horizontalpodautoscaler", true, schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}},
		{"unknownkind", false, schema.GroupVersionResource{}},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			gvr, ok := LookupGVR(tt.kind)
			if ok != tt.wantOK {
				t.Errorf("LookupGVR(%q) ok = %v, want %v", tt.kind, ok, tt.wantOK)
			}
			if ok && gvr != tt.wantGVR {
				t.Errorf("LookupGVR(%q) = %v, want %v", tt.kind, gvr, tt.wantGVR)
			}
		})
	}
}

func TestIsNamespaced(t *testing.T) {
	tests := []struct {
		kind     string
		expected bool
	}{
		{"deployment", true},
		{"pod", true},
		{"service", true},
		{"configmap", true},
		{"secret", true},
		{"ingress", true},
		// Cluster-scoped
		{"namespace", false},
		{"ns", false},
		{"clusterrole", false},
		{"clusterrolebinding", false},
		{"clusterissuer", false},
		{"gatewayclass", false},
		{"gc", false},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			got := IsNamespaced(tt.kind)
			if got != tt.expected {
				t.Errorf("IsNamespaced(%q) = %v, want %v", tt.kind, got, tt.expected)
			}
		})
	}
}

func TestParseYAMLToUnstructured(t *testing.T) {
	t.Run("valid deployment YAML", func(t *testing.T) {
		yaml := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  namespace: default
spec:
  replicas: 3
`)
		obj, err := ParseYAMLToUnstructured(yaml)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if obj.GetName() != "nginx" {
			t.Errorf("expected name nginx, got %s", obj.GetName())
		}
		if obj.GetNamespace() != "default" {
			t.Errorf("expected namespace default, got %s", obj.GetNamespace())
		}
		if obj.GetKind() != "Deployment" {
			t.Errorf("expected kind Deployment, got %s", obj.GetKind())
		}
	})

	t.Run("valid service YAML", func(t *testing.T) {
		yaml := []byte(`apiVersion: v1
kind: Service
metadata:
  name: my-svc
`)
		obj, err := ParseYAMLToUnstructured(yaml)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if obj.GetKind() != "Service" {
			t.Errorf("expected kind Service, got %s", obj.GetKind())
		}
	})

	t.Run("invalid YAML", func(t *testing.T) {
		yaml := []byte(`{{{invalid yaml`)
		_, err := ParseYAMLToUnstructured(yaml)
		if err == nil {
			t.Error("expected error for invalid YAML")
		}
	})

	t.Run("empty YAML", func(t *testing.T) {
		obj, err := ParseYAMLToUnstructured([]byte(""))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if obj.Object != nil {
			t.Error("expected nil Object for empty YAML")
		}
	})
}

func TestGVKToGVR(t *testing.T) {
	tests := []struct {
		name    string
		gvk     schema.GroupVersionKind
		wantGVR schema.GroupVersionResource
	}{
		{
			name:    "known kind deployment",
			gvk:     schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
			wantGVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		},
		{
			name:    "known kind service",
			gvk:     schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"},
			wantGVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"},
		},
		{
			name:    "unknown kind simple plural",
			gvk:     schema.GroupVersionKind{Group: "custom.io", Version: "v1", Kind: "Widget"},
			wantGVR: schema.GroupVersionResource{Group: "custom.io", Version: "v1", Resource: "widgets"},
		},
		{
			name:    "unknown kind ending in s",
			gvk:     schema.GroupVersionKind{Group: "custom.io", Version: "v1", Kind: "Class"},
			wantGVR: schema.GroupVersionResource{Group: "custom.io", Version: "v1", Resource: "classes"},
		},
		{
			name:    "unknown kind ending in ch",
			gvk:     schema.GroupVersionKind{Group: "custom.io", Version: "v1", Kind: "Batch"},
			wantGVR: schema.GroupVersionResource{Group: "custom.io", Version: "v1", Resource: "batches"},
		},
		{
			name:    "unknown kind ending in sh",
			gvk:     schema.GroupVersionKind{Group: "custom.io", Version: "v1", Kind: "Mesh"},
			wantGVR: schema.GroupVersionResource{Group: "custom.io", Version: "v1", Resource: "meshes"},
		},
		{
			name:    "unknown kind ending in x",
			gvk:     schema.GroupVersionKind{Group: "custom.io", Version: "v1", Kind: "Box"},
			wantGVR: schema.GroupVersionResource{Group: "custom.io", Version: "v1", Resource: "boxes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GVKToGVR(tt.gvk)
			if got != tt.wantGVR {
				t.Errorf("GVKToGVR(%v) = %v, want %v", tt.gvk, got, tt.wantGVR)
			}
		})
	}
}

func TestParseAPIVersion(t *testing.T) {
	tests := []struct {
		input       string
		wantGroup   string
		wantVersion string
	}{
		{"v1", "", "v1"},
		{"apps/v1", "apps", "v1"},
		{"networking.k8s.io/v1", "networking.k8s.io", "v1"},
		{"gateway.networking.k8s.io/v1beta1", "gateway.networking.k8s.io", "v1beta1"},
		{"cert-manager.io/v1", "cert-manager.io", "v1"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			group, version := ParseAPIVersion(tt.input)
			if group != tt.wantGroup {
				t.Errorf("ParseAPIVersion(%q) group = %q, want %q", tt.input, group, tt.wantGroup)
			}
			if version != tt.wantVersion {
				t.Errorf("ParseAPIVersion(%q) version = %q, want %q", tt.input, version, tt.wantVersion)
			}
		})
	}
}

func TestBuildGVRFromKindAndAPIVersion(t *testing.T) {
	t.Run("known kind without apiVersion", func(t *testing.T) {
		gvr, ok := BuildGVRFromKindAndAPIVersion("deployment", "")
		if !ok {
			t.Fatal("expected ok=true for known kind")
		}
		if gvr.Resource != "deployments" {
			t.Errorf("expected resource 'deployments', got %q", gvr.Resource)
		}
	})

	t.Run("unknown kind without apiVersion", func(t *testing.T) {
		_, ok := BuildGVRFromKindAndAPIVersion("unknownthing", "")
		if ok {
			t.Error("expected ok=false for unknown kind without apiVersion")
		}
	})

	t.Run("known kind with apiVersion", func(t *testing.T) {
		gvr, ok := BuildGVRFromKindAndAPIVersion("deployment", "apps/v1")
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gvr.Group != "apps" || gvr.Version != "v1" || gvr.Resource != "deployments" {
			t.Errorf("unexpected GVR: %v", gvr)
		}
	})

	t.Run("unknown kind with apiVersion uses pluralization", func(t *testing.T) {
		gvr, ok := BuildGVRFromKindAndAPIVersion("widget", "custom.io/v1")
		if !ok {
			t.Fatal("expected ok=true when apiVersion provided")
		}
		if gvr.Group != "custom.io" || gvr.Version != "v1" || gvr.Resource != "widgets" {
			t.Errorf("unexpected GVR: %v", gvr)
		}
	})

	t.Run("alias with apiVersion", func(t *testing.T) {
		gvr, ok := BuildGVRFromKindAndAPIVersion("deploy", "apps/v1")
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gvr.Resource != "deployments" {
			t.Errorf("expected resource 'deployments', got %q", gvr.Resource)
		}
	})

	t.Run("core resource with v1 apiVersion", func(t *testing.T) {
		gvr, ok := BuildGVRFromKindAndAPIVersion("pod", "v1")
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gvr.Group != "" || gvr.Version != "v1" || gvr.Resource != "pods" {
			t.Errorf("unexpected GVR: %v", gvr)
		}
	})
}
