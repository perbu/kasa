# Kubernetes ConfigMap

ConfigMaps store non-confidential configuration data as key-value pairs.

## Key Fields

| Field | Type | Description |
|-------|------|-------------|
| `data` | map[string]string | UTF-8 text configuration |
| `binaryData` | map[string][]byte | Binary data (base64-encoded) |
| `immutable` | bool | If true, data cannot be updated |

## Basic ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  namespace: production
data:
  # Simple key-value pairs
  APP_ENV: production
  LOG_LEVEL: info
  MAX_CONNECTIONS: "100"

  # Configuration file content
  config.json: |
    {
      "debug": false,
      "logLevel": "info",
      "maxRetries": 3
    }

  nginx.conf: |
    server {
      listen 80;
      location / {
        proxy_pass http://backend:8080;
      }
    }
```

## Using ConfigMaps in Pods

### Single Environment Variable

```yaml
env:
- name: LOG_LEVEL
  valueFrom:
    configMapKeyRef:
      name: app-config
      key: LOG_LEVEL
```

### All Keys as Environment Variables

```yaml
envFrom:
- configMapRef:
    name: app-config
```

### Volume Mount (for config files)

```yaml
volumes:
- name: config
  configMap:
    name: app-config
    items:
    - key: config.json
      path: config.json
    - key: nginx.conf
      path: nginx.conf

volumeMounts:
- name: config
  mountPath: /etc/app
  readOnly: true
```

### Mount Single File

```yaml
volumes:
- name: config
  configMap:
    name: app-config
volumeMounts:
- name: config
  mountPath: /etc/app/config.json
  subPath: config.json
```

## Notes

- Key names must be alphanumeric, `-`, `_`, or `.`
- Keys in `data` and `binaryData` cannot overlap
- Size limit is typically 1MB
- Use Secrets for sensitive data, not ConfigMaps
