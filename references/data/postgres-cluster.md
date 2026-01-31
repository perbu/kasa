# CloudNativePG Cluster

A Cluster resource creates and manages a PostgreSQL cluster with automatic failover, backups, and replication.

## Required Fields

- `spec.instances` - Number of PostgreSQL instances
- `spec.storage.size` - Storage size per instance

## Key Spec Fields

| Field | Type | Description |
|-------|------|-------------|
| `instances` | int | Number of replicas (1 = standalone, 3+ = HA) |
| `storage.size` | string | Storage size (e.g., "10Gi") |
| `storage.storageClass` | string | Storage class (optional, uses default) |
| `imageName` | string | PostgreSQL image (optional) |
| `postgresql.parameters` | map | PostgreSQL configuration |
| `bootstrap` | object | Initialization method |
| `superuserSecret` | object | Custom superuser credentials |

## Minimal Cluster

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: myapp-db
  namespace: production
spec:
  instances: 3
  storage:
    size: 10Gi
```

## Production Cluster

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: myapp-db
  namespace: production
spec:
  instances: 3

  imageName: ghcr.io/cloudnative-pg/postgresql:16.2

  storage:
    size: 50Gi
    storageClass: fast-ssd

  resources:
    requests:
      memory: 1Gi
      cpu: 500m
    limits:
      memory: 2Gi
      cpu: 2

  postgresql:
    parameters:
      max_connections: "200"
      shared_buffers: "256MB"
      effective_cache_size: "1GB"
      work_mem: "16MB"

  bootstrap:
    initdb:
      database: myapp
      owner: myapp
      secret:
        name: myapp-db-credentials
```

## With Backup to S3

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: myapp-db
spec:
  instances: 3
  storage:
    size: 50Gi

  backup:
    barmanObjectStore:
      destinationPath: s3://my-bucket/postgres-backups/myapp
      s3Credentials:
        accessKeyId:
          name: s3-credentials
          key: ACCESS_KEY_ID
        secretAccessKey:
          name: s3-credentials
          key: SECRET_ACCESS_KEY
      wal:
        compression: gzip
    retentionPolicy: "30d"
```

## Custom Superuser Credentials

By default, CloudNativePG generates random credentials. To specify:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: myapp-db-superuser
type: kubernetes.io/basic-auth
stringData:
  username: postgres
  password: supersecretpassword
---
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: myapp-db
spec:
  instances: 3
  storage:
    size: 10Gi
  superuserSecret:
    name: myapp-db-superuser
```

## Bootstrap with Initial Database

```yaml
bootstrap:
  initdb:
    database: myapp           # Create this database
    owner: myapp              # Owner role
    secret:                   # Credentials for owner
      name: myapp-db-credentials
    postInitSQL:
    - CREATE EXTENSION IF NOT EXISTS "uuid-ossp"
    - CREATE EXTENSION IF NOT EXISTS "pg_stat_statements"
```

## Connection Details

CloudNativePG creates several Services:

| Service | Purpose |
|---------|---------|
| `<cluster>-rw` | Read-write (primary) |
| `<cluster>-ro` | Read-only (replicas) |
| `<cluster>-r` | Any instance |

Connection string format:
```
postgres://<user>:<password>@<cluster>-rw.<namespace>:5432/<database>
```

## Generated Secrets

CloudNativePG creates these Secrets automatically:

| Secret | Contents |
|--------|----------|
| `<cluster>-superuser` | postgres superuser credentials |
| `<cluster>-app` | Application user credentials |

## Cluster Status

```bash
kubectl get cluster myapp-db
kubectl describe cluster myapp-db
```

Key status fields:
- `phase: Cluster in healthy state` - All instances running
- `instances` - Current instance count
- `readyInstances` - Healthy instances

## Notes

- Always use 3+ instances for production (enables HA with automatic failover)
- Specify exact image versions, not `latest`
- Use `-rw` service for writes, `-ro` for read replicas
- Secrets are created in the same namespace as the Cluster
