# Kubernetes Deployment

A Deployment manages a set of replicated Pods, handling rolling updates and rollbacks.

## Required Fields

- `spec.selector` - Label selector matching the pod template
- `spec.template` - Pod template with matching labels

## Key Spec Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `replicas` | int | 1 | Desired number of pods |
| `selector.matchLabels` | map | - | Must match template labels |
| `strategy.type` | string | RollingUpdate | `RollingUpdate` or `Recreate` |
| `strategy.rollingUpdate.maxSurge` | int/% | 25% | Max pods above desired during update |
| `strategy.rollingUpdate.maxUnavailable` | int/% | 25% | Max unavailable during update |
| `minReadySeconds` | int | 0 | Seconds before pod considered available |
| `revisionHistoryLimit` | int | 10 | Old ReplicaSets to retain |

## Container Spec

```yaml
containers:
- name: app
  image: myapp:v1.0.0
  ports:
  - containerPort: 8080
  env:
  - name: DATABASE_URL
    valueFrom:
      secretKeyRef:
        name: db-credentials
        key: url
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 512Mi
  livenessProbe:
    httpGet:
      path: /healthz
      port: 8080
    initialDelaySeconds: 10
    periodSeconds: 10
  readinessProbe:
    httpGet:
      path: /ready
      port: 8080
    initialDelaySeconds: 5
    periodSeconds: 5
```

## Complete Example

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
  namespace: production
  labels:
    app: myapp
spec:
  replicas: 3
  selector:
    matchLabels:
      app: myapp
  template:
    metadata:
      labels:
        app: myapp
    spec:
      containers:
      - name: myapp
        image: ghcr.io/org/myapp:v1.2.3
        ports:
        - containerPort: 8080
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
        env:
        - name: PORT
          value: "8080"
      imagePullSecrets:
      - name: ghcr-credentials
```

## Probes

| Probe | Purpose | Action on Failure |
|-------|---------|-------------------|
| `livenessProbe` | Is container alive? | Restart container |
| `readinessProbe` | Can container serve traffic? | Remove from Service endpoints |
| `startupProbe` | Has container started? | Delay liveness/readiness checks |

## Probe Configuration

```yaml
httpGet:
  path: /healthz
  port: 8080
initialDelaySeconds: 10  # Wait before first probe
periodSeconds: 10        # How often to probe
timeoutSeconds: 1        # Probe timeout
successThreshold: 1      # Successes to be considered healthy
failureThreshold: 3      # Failures before action taken
```
