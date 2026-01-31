# Gateway API HTTPRoute

HTTPRoute defines HTTP routing rules to backend services. It attaches to a Gateway via `parentRefs`.

## Required Fields

- `spec.parentRefs` - Gateway(s) this route attaches to
- `spec.rules` - Routing rules with matches and backends

## Key Spec Fields

| Field | Type | Description |
|-------|------|-------------|
| `parentRefs` | array | Gateways to attach to |
| `hostnames` | array | HTTP Host header matches |
| `rules` | array | Routing rules |
| `rules[].matches` | array | Request matching conditions |
| `rules[].backendRefs` | array | Target services |
| `rules[].filters` | array | Request/response modifications |

## Basic HTTPRoute

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: myapp
  namespace: production
spec:
  parentRefs:
  - name: main-gateway
    namespace: gateway-system
  hostnames:
  - myapp.example.com
  rules:
  - backendRefs:
    - name: myapp
      port: 80
```

## Path-Based Routing

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: myapp
spec:
  parentRefs:
  - name: main-gateway
  hostnames:
  - api.example.com
  rules:
  # /api/* -> api-service
  - matches:
    - path:
        type: PathPrefix
        value: /api
    backendRefs:
    - name: api-service
      port: 8080

  # /static/* -> static-service
  - matches:
    - path:
        type: PathPrefix
        value: /static
    backendRefs:
    - name: static-service
      port: 80

  # Default -> frontend
  - backendRefs:
    - name: frontend
      port: 80
```

## Header-Based Routing

```yaml
rules:
- matches:
  - headers:
    - name: X-Version
      value: v2
  backendRefs:
  - name: myapp-v2
    port: 80
- backendRefs:
  - name: myapp-v1
    port: 80
```

## Weighted Traffic Split

```yaml
rules:
- backendRefs:
  - name: myapp-v1
    port: 80
    weight: 90
  - name: myapp-v2
    port: 80
    weight: 10
```

## Request Redirect

```yaml
rules:
- matches:
  - path:
      type: PathPrefix
      value: /old-path
  filters:
  - type: RequestRedirect
    requestRedirect:
      path:
        type: ReplaceFullPath
        replaceFullPath: /new-path
      statusCode: 301
```

## Path Rewrite

```yaml
rules:
- matches:
  - path:
      type: PathPrefix
      value: /api/v1
  filters:
  - type: URLRewrite
    urlRewrite:
      path:
        type: ReplacePrefixMatch
        replacePrefixMatch: /v1
  backendRefs:
  - name: api-service
    port: 8080
```

## Match Types

### Path Match Types

| Type | Description |
|------|-------------|
| `Exact` | Exact path match |
| `PathPrefix` | Prefix match (default if unspecified) |
| `RegularExpression` | Regex match (implementation-specific) |

### Header Match Types

| Type | Description |
|------|-------------|
| `Exact` | Exact header value match (default) |
| `RegularExpression` | Regex match |

## Notes

- Routes attach to Gateways; the Gateway must allow the route's namespace
- If no matches specified, rule matches all requests
- Rules are evaluated in order; first match wins
- Multiple hostnames can be specified; route matches any of them
