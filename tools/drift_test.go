package tools

import (
	"testing"
)

func TestDiffMaps_Identical(t *testing.T) {
	a := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "nginx",
			"namespace": "default",
		},
	}
	b := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "nginx",
			"namespace": "default",
		},
	}

	diffs := DiffMaps(a, b, "")
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for identical maps, got %d: %+v", len(diffs), diffs)
	}
}

func TestDiffMaps_ChangedScalar(t *testing.T) {
	stored := map[string]any{
		"spec": map[string]any{
			"replicas": float64(3),
			"image":    "nginx:1.24",
		},
	}
	live := map[string]any{
		"spec": map[string]any{
			"replicas": float64(5),
			"image":    "nginx:1.24",
		},
	}

	diffs := DiffMaps(stored, live, "")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}

	d := diffs[0]
	if d.Path != "spec.replicas" {
		t.Errorf("expected path 'spec.replicas', got %q", d.Path)
	}
	if d.ChangeType != "changed" {
		t.Errorf("expected change_type 'changed', got %q", d.ChangeType)
	}
	if d.Stored != float64(3) {
		t.Errorf("expected stored=3, got %v", d.Stored)
	}
	if d.Live != float64(5) {
		t.Errorf("expected live=5, got %v", d.Live)
	}
}

func TestDiffMaps_AddedField(t *testing.T) {
	stored := map[string]any{
		"metadata": map[string]any{
			"name": "nginx",
		},
	}
	live := map[string]any{
		"metadata": map[string]any{
			"name": "nginx",
			"labels": map[string]any{
				"app": "nginx",
			},
		},
	}

	diffs := DiffMaps(stored, live, "")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}

	d := diffs[0]
	if d.Path != "metadata.labels" {
		t.Errorf("expected path 'metadata.labels', got %q", d.Path)
	}
	if d.ChangeType != "added" {
		t.Errorf("expected change_type 'added', got %q", d.ChangeType)
	}
}

func TestDiffMaps_RemovedField(t *testing.T) {
	stored := map[string]any{
		"metadata": map[string]any{
			"name": "nginx",
			"labels": map[string]any{
				"app": "nginx",
			},
		},
	}
	live := map[string]any{
		"metadata": map[string]any{
			"name": "nginx",
		},
	}

	diffs := DiffMaps(stored, live, "")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}

	d := diffs[0]
	if d.Path != "metadata.labels" {
		t.Errorf("expected path 'metadata.labels', got %q", d.Path)
	}
	if d.ChangeType != "removed" {
		t.Errorf("expected change_type 'removed', got %q", d.ChangeType)
	}
}

func TestDiffMaps_NestedMaps(t *testing.T) {
	stored := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "nginx",
							"image": "nginx:1.24",
						},
					},
				},
			},
		},
	}
	live := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "nginx",
							"image": "nginx:1.25",
						},
					},
				},
			},
		},
	}

	diffs := DiffMaps(stored, live, "")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}

	d := diffs[0]
	if d.Path != "spec.template.spec.containers[0].image" {
		t.Errorf("expected path 'spec.template.spec.containers[0].image', got %q", d.Path)
	}
	if d.ChangeType != "changed" {
		t.Errorf("expected change_type 'changed', got %q", d.ChangeType)
	}
}

func TestDiffMaps_SliceDifferentLengths(t *testing.T) {
	stored := map[string]any{
		"containers": []any{
			map[string]any{"name": "a"},
		},
	}
	live := map[string]any{
		"containers": []any{
			map[string]any{"name": "a"},
			map[string]any{"name": "b"},
		},
	}

	diffs := DiffMaps(stored, live, "")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}

	d := diffs[0]
	if d.Path != "containers[1]" {
		t.Errorf("expected path 'containers[1]', got %q", d.Path)
	}
	if d.ChangeType != "added" {
		t.Errorf("expected change_type 'added', got %q", d.ChangeType)
	}
}

func TestDiffMaps_EmptyVsPopulated(t *testing.T) {
	stored := map[string]any{}
	live := map[string]any{
		"status": "running",
	}

	diffs := DiffMaps(stored, live, "")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}

	d := diffs[0]
	if d.Path != "status" {
		t.Errorf("expected path 'status', got %q", d.Path)
	}
	if d.ChangeType != "added" {
		t.Errorf("expected change_type 'added', got %q", d.ChangeType)
	}
}

func TestDiffMaps_WithPrefix(t *testing.T) {
	stored := map[string]any{"image": "nginx:1.24"}
	live := map[string]any{"image": "nginx:1.25"}

	diffs := DiffMaps(stored, live, "spec.containers[0]")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}

	if diffs[0].Path != "spec.containers[0].image" {
		t.Errorf("expected prefixed path, got %q", diffs[0].Path)
	}
}

func TestDiffMaps_MultipleDiffs(t *testing.T) {
	stored := map[string]any{
		"a": "1",
		"b": "2",
		"c": "3",
	}
	live := map[string]any{
		"a": "1",
		"b": "changed",
		"d": "4",
	}

	diffs := DiffMaps(stored, live, "")
	// b changed, c removed, d added = 3 diffs
	if len(diffs) != 3 {
		t.Fatalf("expected 3 diffs, got %d: %+v", len(diffs), diffs)
	}

	// Diffs should be sorted by key
	paths := make([]string, len(diffs))
	for i, d := range diffs {
		paths[i] = d.Path
	}
	if paths[0] != "b" || paths[1] != "c" || paths[2] != "d" {
		t.Errorf("expected sorted paths [b, c, d], got %v", paths)
	}
}

func TestDiffMaps_NumericTypeNormalization(t *testing.T) {
	// YAML parses 80 as int, JSON roundtrip makes it float64.
	// These should be considered equal.
	stored := map[string]any{
		"spec": map[string]any{
			"ports": []any{
				map[string]any{
					"port":       int(80),
					"targetPort": int64(5678),
				},
			},
		},
	}
	live := map[string]any{
		"spec": map[string]any{
			"ports": []any{
				map[string]any{
					"port":       float64(80),
					"targetPort": float64(5678),
				},
			},
		},
	}

	diffs := DiffMaps(stored, live, "")
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for numerically equal values, got %d: %+v", len(diffs), diffs)
	}
}

func TestDiffMaps_NumericActualDifference(t *testing.T) {
	stored := map[string]any{"replicas": int(3)}
	live := map[string]any{"replicas": float64(5)}

	diffs := DiffMaps(stored, live, "")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].ChangeType != "changed" {
		t.Errorf("expected 'changed', got %q", diffs[0].ChangeType)
	}
}

func TestDiffMaps_SliceShorterLive(t *testing.T) {
	stored := map[string]any{
		"items": []any{"a", "b", "c"},
	}
	live := map[string]any{
		"items": []any{"a"},
	}

	diffs := DiffMaps(stored, live, "")
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d: %+v", len(diffs), diffs)
	}

	if diffs[0].Path != "items[1]" || diffs[0].ChangeType != "removed" {
		t.Errorf("expected items[1] removed, got %+v", diffs[0])
	}
	if diffs[1].Path != "items[2]" || diffs[1].ChangeType != "removed" {
		t.Errorf("expected items[2] removed, got %+v", diffs[1])
	}
}
