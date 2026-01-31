# cert-manager Certificate

A Certificate resource requests an X.509 certificate from an Issuer or ClusterIssuer. cert-manager automatically handles issuance and renewal.

## Required Fields

- `spec.secretName` - Secret to store the certificate
- `spec.issuerRef` - Reference to Issuer or ClusterIssuer
- At least one of: `dnsNames`, `uris`, `emailAddresses`, `ipAddresses`

## Key Spec Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `secretName` | string | - | Secret for cert and key |
| `issuerRef.name` | string | - | Issuer name |
| `issuerRef.kind` | string | Issuer | `Issuer` or `ClusterIssuer` |
| `dnsNames` | array | - | DNS names for the certificate |
| `duration` | duration | 90d | Certificate validity |
| `renewBefore` | duration | 30d | Renew this long before expiry |
| `isCA` | bool | false | Issue as CA certificate |
| `usages` | array | server auth | Key usages |

## Basic Certificate

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: myapp-tls
  namespace: production
spec:
  secretName: myapp-tls
  dnsNames:
  - myapp.example.com
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
```

## Wildcard Certificate

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: wildcard-example-com
  namespace: gateway-system
spec:
  secretName: wildcard-example-com
  dnsNames:
  - "*.example.com"
  - example.com
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
```

## Certificate with Custom Duration

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: myapp-tls
spec:
  secretName: myapp-tls
  duration: 2160h      # 90 days
  renewBefore: 360h    # 15 days before expiry
  dnsNames:
  - myapp.example.com
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
```

## Multiple DNS Names

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: myapp-tls
spec:
  secretName: myapp-tls
  dnsNames:
  - myapp.example.com
  - www.myapp.example.com
  - api.myapp.example.com
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
```

## Private Key Configuration

```yaml
spec:
  secretName: myapp-tls
  privateKey:
    algorithm: ECDSA
    size: 256
    rotationPolicy: Always  # Rotate key on each renewal
  dnsNames:
  - myapp.example.com
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
```

## Resulting Secret

cert-manager creates a Secret with:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: myapp-tls
type: kubernetes.io/tls
data:
  tls.crt: <base64 certificate chain>
  tls.key: <base64 private key>
  ca.crt: <base64 CA certificate>  # if available
```

## Certificate Status

Check certificate status:
```bash
kubectl get certificate myapp-tls
kubectl describe certificate myapp-tls
```

Key status fields:
- `Ready` - True when certificate is issued
- `notAfter` - Expiration time
- `renewalTime` - When renewal will be attempted

## Notes

- Wildcard certificates require DNS-01 challenge (not HTTP-01)
- Certificate must be in same namespace as the workload using it
- For Gateway API, certificate Secret must be in Gateway's namespace
- Use ReferenceGrant to allow cross-namespace Secret references
