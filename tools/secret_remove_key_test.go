package tools

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestRemoveSecretKeyTool tests the remove_secret_key tool.
func TestRemoveSecretKeyTool(t *testing.T) {
	tool := NewRemoveSecretKeyTool(clientset)
	nsName := "test-remove-secret-key"
	createTestNamespace(t, clientset, nsName)

	t.Run("removes a single key and keeps the rest", func(t *testing.T) {
		createTestSecret(t, clientset, nsName, "multi-key", map[string][]byte{
			"password": []byte("hunter2"),
			"api-key":  []byte("abc123"),
			"host":     []byte("db.example.com"),
		})

		result, err := tool.Run(nil, map[string]any{
			"name":      "multi-key",
			"namespace": nsName,
			"keys":      []any{"api-key"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; ok {
			t.Fatalf("tool returned error: %v", result["error"])
		}

		if got := result["removed_keys"].([]string); !reflect.DeepEqual(got, []string{"api-key"}) {
			t.Errorf("expected removed_keys [api-key], got %v", got)
		}
		if got := result["remaining_keys"].([]string); !reflect.DeepEqual(got, []string{"host", "password"}) {
			t.Errorf("expected remaining_keys [host password], got %v", got)
		}

		// Verify against the cluster, not just the tool's own report.
		secret, err := clientset.CoreV1().Secrets(nsName).Get(t.Context(), "multi-key", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("failed to read back secret: %v", err)
		}
		if _, ok := secret.Data["api-key"]; ok {
			t.Error("expected api-key to be gone from the secret")
		}
		if string(secret.Data["password"]) != "hunter2" {
			t.Errorf("expected password to survive, got %q", secret.Data["password"])
		}
	})

	t.Run("removes several keys and reports missing ones", func(t *testing.T) {
		createTestSecret(t, clientset, nsName, "partial", map[string][]byte{
			"a": []byte("1"),
			"b": []byte("2"),
			"c": []byte("3"),
		})

		result, err := tool.Run(nil, map[string]any{
			"name":      "partial",
			"namespace": nsName,
			"keys":      []any{"a", "b", "nope"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; ok {
			t.Fatalf("tool returned error: %v", result["error"])
		}

		if got := result["removed_keys"].([]string); !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Errorf("expected removed_keys [a b], got %v", got)
		}
		if got := result["missing_keys"].([]string); !reflect.DeepEqual(got, []string{"nope"}) {
			t.Errorf("expected missing_keys [nope], got %v", got)
		}
		if got := result["remaining_keys"].([]string); !reflect.DeepEqual(got, []string{"c"}) {
			t.Errorf("expected remaining_keys [c], got %v", got)
		}
	})

	t.Run("notes when the secret is left empty", func(t *testing.T) {
		createTestSecret(t, clientset, nsName, "only-key", map[string][]byte{"solo": []byte("x")})

		result, err := tool.Run(nil, map[string]any{
			"name":      "only-key",
			"namespace": nsName,
			"keys":      []any{"solo"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["note"]; !ok {
			t.Errorf("expected a note about the empty secret, got: %v", result)
		}
	})

	t.Run("errors when no requested key exists", func(t *testing.T) {
		createTestSecret(t, clientset, nsName, "untouched", map[string][]byte{"keep": []byte("me")})

		result, err := tool.Run(nil, map[string]any{
			"name":      "untouched",
			"namespace": nsName,
			"keys":      []any{"missing"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; !ok {
			t.Errorf("expected an error result, got: %v", result)
		}

		secret, err := clientset.CoreV1().Secrets(nsName).Get(t.Context(), "untouched", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("failed to read back secret: %v", err)
		}
		if string(secret.Data["keep"]) != "me" {
			t.Error("expected the secret to be left untouched")
		}
	})

	t.Run("errors on a missing secret", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":      "does-not-exist",
			"namespace": nsName,
			"keys":      []any{"a"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; !ok {
			t.Errorf("expected an error result, got: %v", result)
		}
	})

	t.Run("validates arguments", func(t *testing.T) {
		cases := []struct {
			name string
			args map[string]any
		}{
			{"missing name", map[string]any{"namespace": nsName, "keys": []any{"a"}}},
			{"missing namespace", map[string]any{"name": "x", "keys": []any{"a"}}},
			{"missing keys", map[string]any{"name": "x", "namespace": nsName}},
			{"empty keys", map[string]any{"name": "x", "namespace": nsName, "keys": []any{}}},
			{"non-string key", map[string]any{"name": "x", "namespace": nsName, "keys": []any{1}}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				result, err := tool.Run(nil, tc.args)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if _, ok := result["error"]; !ok {
					t.Errorf("expected an error result, got: %v", result)
				}
			})
		}
	})
}

func TestEscapeJSONPointer(t *testing.T) {
	cases := map[string]string{
		"password": "password",
		"a/b":      "a~1b",
		"a~b":      "a~0b",
		"a~/b":     "a~0~1b",
		"tls.crt":  "tls.crt",
	}
	for in, want := range cases {
		if got := escapeJSONPointer(in); got != want {
			t.Errorf("escapeJSONPointer(%q) = %q, want %q", in, got, want)
		}
	}
}
