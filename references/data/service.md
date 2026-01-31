# Kubernetes Service

A Service provides stable network identity and load balancing for a set of Pods.

## Required Fields

- `spec.ports[].port` - The port the service exposes (1-65535)

## Service Types

| Type | Description | Use Case |
|------|-------------|----------|
| `ClusterIP` | Internal cluster IP only | Default, internal services |
| `NodePort` | Expose on each node's IP at a static port | External access without LB |
| `LoadBalancer` | Provision external load balancer | Cloud environments |
| `ExternalName` | CNAME alias to external service | External service mapping |

## Key Spec Fields

| Field | Type | Description |
|-------|------|-------------|
| `selector` | map | Pod labels to route traffic to |
| `ports` | array | Port mappings |
| `type` | string | Service type (default: ClusterIP) |
| `clusterIP` | string | Cluster IP (or "None" for headless) |

## Port Configuration

```yaml
ports:
- name: http           # Required if multiple ports
  protocol: TCP        # TCP (default), UDP, or SCTP
  port: 80             # Service port (required)
  targetPort: 8080     # Pod port (defaults to port)
  appProtocol: http    # Optional protocol hint
```

## ClusterIP Example (Most Common)

```yaml
apiVersion: v1
kind: Service
metadata:
  name: myapp
  namespace: production
spec:
  type: ClusterIP
  selector:
    app: myapp
  ports:
  - name: http
    port: 80
    targetPort: 8080
    protocol: TCP
```

## Headless Service (for StatefulSets)

```yaml
apiVersion: v1
kind: Service
metadata:
  name: postgres-headless
spec:
  type: ClusterIP
  clusterIP: None
  selector:
    app: postgres
  ports:
  - port: 5432
```

## Multi-Port Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: myapp
spec:
  selector:
    app: myapp
  ports:
  - name: http
    port: 80
    targetPort: 8080
  - name: metrics
    port: 9090
    targetPort: 9090
```

## Notes

- When using Gateway API (HTTPRoute), use ClusterIP services - the Gateway handles external exposure
- Selector must match pod labels exactly
- If selector is omitted, you must manually create Endpoints
