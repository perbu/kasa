# Gateway API Gateway

A Gateway represents infrastructure that handles traffic (like a load balancer or proxy). HTTPRoutes attach to Gateways to define routing rules.

## Required Fields

- `spec.gatewayClassName` - Reference to GatewayClass
- `spec.listeners` - Ports and protocols to accept

## Key Spec Fields

| Field | Type | Description |
|-------|------|-------------|
| `gatewayClassName` | string | GatewayClass implementing this Gateway |
| `listeners` | array | Listener configurations |
| `addresses` | array | Requested addresses (optional) |

## Listener Configuration

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique listener name |
| `port` | int | Port number (1-65535) |
| `protocol` | string | HTTP, HTTPS, TLS, TCP, UDP |
| `hostname` | string | Optional hostname filter |
| `tls` | object | TLS configuration (for HTTPS/TLS) |
| `allowedRoutes` | object | Which routes can attach |

## Basic Gateway (HTTP)

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: main-gateway
  namespace: gateway-system
spec:
  gatewayClassName: envoy-gateway
  listeners:
  - name: http
    port: 80
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: All
```

## HTTPS Gateway with TLS

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: main-gateway
  namespace: gateway-system
spec:
  gatewayClassName: envoy-gateway
  listeners:
  - name: https
    port: 443
    protocol: HTTPS
    hostname: "*.example.com"
    tls:
      mode: Terminate
      certificateRefs:
      - name: wildcard-example-com
        kind: Secret
    allowedRoutes:
      namespaces:
        from: All
```

## Multi-Listener Gateway

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: main-gateway
spec:
  gatewayClassName: envoy-gateway
  listeners:
  # HTTP -> HTTPS redirect handled by routes
  - name: http
    port: 80
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: All

  # Production traffic
  - name: https-prod
    port: 443
    protocol: HTTPS
    hostname: "*.example.com"
    tls:
      certificateRefs:
      - name: prod-wildcard
    allowedRoutes:
      namespaces:
        from: Selector
        selector:
          matchLabels:
            environment: production

  # Staging traffic
  - name: https-staging
    port: 443
    protocol: HTTPS
    hostname: "*.staging.example.com"
    tls:
      certificateRefs:
      - name: staging-wildcard
    allowedRoutes:
      namespaces:
        from: Selector
        selector:
          matchLabels:
            environment: staging
```

## AllowedRoutes Configuration

```yaml
allowedRoutes:
  namespaces:
    from: All           # All namespaces
    # OR
    from: Same          # Same namespace as Gateway
    # OR
    from: Selector      # Namespaces matching selector
    selector:
      matchLabels:
        gateway-access: "true"
  kinds:
  - kind: HTTPRoute
  - kind: GRPCRoute
```

## GatewayClass

GatewayClass defines the controller that implements Gateways. Usually created by cluster admin.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: envoy-gateway
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
```

Common GatewayClass controllers:
- `gateway.envoyproxy.io/gatewayclass-controller` (Envoy Gateway)
- `istio.io/gateway-controller` (Istio)
- `cilium.io/gateway-controller` (Cilium)

## TLS Modes

| Mode | Description |
|------|-------------|
| `Terminate` | Gateway terminates TLS, forwards HTTP to backends |
| `Passthrough` | Gateway passes TLS directly to backends |

## Notes

- Gateway and GatewayClass are typically in a dedicated namespace (e.g., `gateway-system`)
- HTTPRoutes reference Gateways via `parentRefs`
- Certificate Secrets must be in same namespace as Gateway, or use ReferenceGrant
- Multiple listeners can share a port if they have different hostnames
