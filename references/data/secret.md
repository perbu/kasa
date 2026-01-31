# Kubernetes Secret

Secrets store sensitive data like passwords, tokens, and keys. Values are base64-encoded (not encrypted at rest by default).

## Secret Types

| Type | Purpose | Required Keys |
|------|---------|---------------|
| `Opaque` | Arbitrary user data (default) | None |
| `kubernetes.io/tls` | TLS certificate and key | `tls.crt`, `tls.key` |
| `kubernetes.io/basic-auth` | Basic authentication | `username`, `password` |
| `kubernetes.io/ssh-auth` | SSH credentials | `ssh-privatekey` |
| `kubernetes.io/dockerconfigjson` | Docker registry auth | `.dockerconfigjson` |

## Key Fields

| Field | Type | Description |
|-------|------|-------------|
| `data` | map | Base64-encoded values |
| `stringData` | map | Plain text (write-only, converted to base64) |
| `type` | string | Secret type identifier |
| `immutable` | bool | If true, data cannot be updated |

## Opaque Secret (General Purpose)

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: db-credentials
  namespace: production
type: Opaque
stringData:
  username: appuser
  password: secretpassword123
  url: postgres://appuser:secretpassword123@postgres:5432/mydb
```

## TLS Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: myapp-tls
type: kubernetes.io/tls
data:
  tls.crt: LS0tLS1CRUdJTi...  # base64-encoded certificate
  tls.key: LS0tLS1CRUdJTi...  # base64-encoded private key
```

## Docker Registry Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ghcr-credentials
type: kubernetes.io/dockerconfigjson
stringData:
  .dockerconfigjson: |
    {
      "auths": {
        "ghcr.io": {
          "username": "myuser",
          "password": "ghp_token",
          "auth": "bXl1c2VyOmdocF90b2tlbg=="
        }
      }
    }
```

## Using Secrets in Pods

### Environment Variable

```yaml
env:
- name: DATABASE_PASSWORD
  valueFrom:
    secretKeyRef:
      name: db-credentials
      key: password
```

### All Keys as Environment Variables

```yaml
envFrom:
- secretRef:
    name: db-credentials
```

### Volume Mount

```yaml
volumes:
- name: secrets
  secret:
    secretName: db-credentials
volumeMounts:
- name: secrets
  mountPath: /etc/secrets
  readOnly: true
```

## Notes

- `stringData` is write-only; reading the secret returns base64 in `data`
- Total secret size must be less than 1MB
- Consider external-secrets or sealed-secrets for GitOps workflows
