package repl

import (
	"strings"
	"testing"
)

// TestDyffDiffRename exercises an env var rename
// (SALESFORCE_DOMAIN -> SALESFORCE_BASE_URL) — both sides must appear.
func TestDyffDiffRename(t *testing.T) {
	cluster := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: PHOENIX_API_KEY
          valueFrom:
            secretKeyRef:
              key: PHOENIX_API_KEY
              name: nexus-secrets
        - name: PHOENIX_URL
          value: http://phoenix.portal.svc.cluster.local/
        - name: SALESFORCE_DOMAIN
          value: varnish.my.salesforce.com
        - name: ZEN_URL
          value: http://zen-api.zen.svc.cluster.local:8081/
        - name: AUTH_MODE
          value: headers
`

	proposed := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: PHOENIX_API_KEY
          valueFrom:
            secretKeyRef:
              key: PHOENIX_API_KEY
              name: nexus-secrets
        - name: PHOENIX_URL
          value: http://phoenix.portal.svc.cluster.local/
        - name: SALESFORCE_BASE_URL
          value: varnish.my.salesforce.com
        - name: ZEN_URL
          value: http://zen-api.zen.svc.cluster.local:8081/
        - name: AUTH_MODE
          value: headers
`

	out, err := dyffDiff(cluster, proposed)
	if err != nil {
		t.Fatalf("dyffDiff: %v", err)
	}
	t.Logf("\n%s", out)

	if !strings.Contains(out, "SALESFORCE_DOMAIN") {
		t.Errorf("expected removed SALESFORCE_DOMAIN to be mentioned, got:\n%s", out)
	}
	if !strings.Contains(out, "SALESFORCE_BASE_URL") {
		t.Errorf("expected added SALESFORCE_BASE_URL to be mentioned, got:\n%s", out)
	}
}

// TestDyffDiffValueToValueFrom is the case the current implementation can't
// fully express: an env var changing from a literal `value` to a
// `valueFrom: secretKeyRef`. Both sides of the change should be visible.
func TestDyffDiffValueToValueFrom(t *testing.T) {
	cluster := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: SENDGRID_KEY
          value: leaked-literal-secret
`

	proposed := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: SENDGRID_KEY
          valueFrom:
            secretKeyRef:
              name: nexus-secrets
              key: SENDGRID_KEY
`

	out, err := dyffDiff(cluster, proposed)
	if err != nil {
		t.Fatalf("dyffDiff: %v", err)
	}
	t.Logf("\n%s", out)

	if !strings.Contains(out, "leaked-literal-secret") {
		t.Errorf("expected old literal value to appear so the user can see it is being removed, got:\n%s", out)
	}
	if !strings.Contains(out, "valueFrom") {
		t.Errorf("expected valueFrom to be mentioned in proposed, got:\n%s", out)
	}
}

// TestDyffDiffInsertion is the original case: inserting a new env var in the
// middle of the list. Should NOT cascade.
func TestDyffDiffInsertion(t *testing.T) {
	cluster := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: PHOENIX_API_KEY
          value: secret-key
        - name: SALESFORCE_DOMAIN
          value: varnish.my.salesforce.com
        - name: ZEN_URL
          value: https://zen.varnish-software.com/
        - name: AUTH_MODE
          value: headers
`

	proposed := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: PHOENIX_API_KEY
          value: secret-key
        - name: PHOENIX_URL
          value: http://phoenix.portal.svc.cluster.local:7080/
        - name: SALESFORCE_DOMAIN
          value: varnish.my.salesforce.com
        - name: ZEN_URL
          value: https://zen.varnish-software.com/
        - name: AUTH_MODE
          value: headers
`

	out, err := dyffDiff(cluster, proposed)
	if err != nil {
		t.Fatalf("dyffDiff: %v", err)
	}
	t.Logf("\n%s", out)

	if !strings.Contains(out, "PHOENIX_URL") {
		t.Errorf("expected PHOENIX_URL to be shown as added, got:\n%s", out)
	}
	if strings.Count(out, "SALESFORCE_DOMAIN") > 1 || strings.Count(out, "ZEN_URL") > 1 || strings.Count(out, "AUTH_MODE") > 1 {
		t.Errorf("unchanged env vars should not appear repeatedly (cascade), got:\n%s", out)
	}
}
