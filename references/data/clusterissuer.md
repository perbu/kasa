# cert-manager ClusterIssuer

A ClusterIssuer is a cluster-scoped resource that issues certificates. Unlike Issuer, it can be referenced from any namespace.

## Required Fields

- `spec.acme.server` - ACME server URL
- `spec.acme.email` - Account email
- `spec.acme.privateKeySecretRef` - Secret for account key
- `spec.acme.solvers` - Challenge solvers

## ACME Servers

| Provider | Staging URL | Production URL |
|----------|-------------|----------------|
| Let's Encrypt | `https://acme-staging-v02.api.letsencrypt.org/directory` | `https://acme-v02.api.letsencrypt.org/directory` |

## HTTP-01 ClusterIssuer

For publicly accessible services. Creates temporary Ingress/HTTPRoute for validation.

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@example.com
    privateKeySecretRef:
      name: letsencrypt-prod-account-key
    solvers:
    - http01:
        gatewayHTTPRoute:
          parentRefs:
          - name: main-gateway
            namespace: gateway-system
```

### With Ingress (if not using Gateway API)

```yaml
solvers:
- http01:
    ingress:
      ingressClassName: nginx
```

## DNS-01 ClusterIssuer (Cloudflare)

Required for wildcard certificates. Validates via DNS TXT records.

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@example.com
    privateKeySecretRef:
      name: letsencrypt-prod-account-key
    solvers:
    - dns01:
        cloudflare:
          apiTokenSecretRef:
            name: cloudflare-api-token
            key: api-token
```

Cloudflare API token Secret:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cloudflare-api-token
  namespace: cert-manager
type: Opaque
stringData:
  api-token: <cloudflare-api-token>
```

## DNS-01 ClusterIssuer (Route53)

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@example.com
    privateKeySecretRef:
      name: letsencrypt-prod-account-key
    solvers:
    - dns01:
        route53:
          region: us-east-1
          hostedZoneID: Z1234567890ABC
          # Uses IRSA or instance profile for auth
```

## Selector-Based Solvers

Use different solvers for different domains:

```yaml
solvers:
# Wildcard certs via DNS-01
- selector:
    dnsNames:
    - "*.example.com"
  dns01:
    cloudflare:
      apiTokenSecretRef:
        name: cloudflare-api-token
        key: api-token

# Specific subdomains via HTTP-01
- selector:
    dnsNames:
    - app.example.com
    - api.example.com
  http01:
    gatewayHTTPRoute:
      parentRefs:
      - name: main-gateway
        namespace: gateway-system

# Default solver for everything else
- http01:
    gatewayHTTPRoute:
      parentRefs:
      - name: main-gateway
        namespace: gateway-system
```

## Staging Issuer (for Testing)

Always test with staging first to avoid rate limits:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-staging
spec:
  acme:
    server: https://acme-staging-v02.api.letsencrypt.org/directory
    email: admin@example.com
    privateKeySecretRef:
      name: letsencrypt-staging-account-key
    solvers:
    - http01:
        gatewayHTTPRoute:
          parentRefs:
          - name: main-gateway
            namespace: gateway-system
```

## Check Issuer Status

```bash
kubectl get clusterissuer
kubectl describe clusterissuer letsencrypt-prod
```

Look for:
- `Ready: True` - Issuer is configured correctly
- `Registered` - Account registered with ACME server

## Notes

- Use ClusterIssuer for cluster-wide certificate issuance
- Use Issuer (namespace-scoped) for namespace-specific configurations
- Staging certs are not trusted by browsers but don't count against rate limits
- DNS-01 is required for wildcard certificates
- HTTP-01 requires port 80 to be publicly accessible
