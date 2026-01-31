# CloudNativePG Database

A Database resource declaratively creates a PostgreSQL database within a CloudNativePG Cluster.

## Required Fields

- `spec.name` - PostgreSQL database name
- `spec.owner` - Role that owns the database
- `spec.cluster.name` - Target Cluster name

## Key Spec Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Database name in PostgreSQL |
| `owner` | string | Owner role name |
| `cluster.name` | string | CloudNativePG Cluster name |
| `databaseReclaimPolicy` | string | `retain` or `delete` on CR deletion |
| `encoding` | string | Character encoding (default: UTF8) |
| `template` | string | Template database |

## Basic Database

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata:
  name: myapp-db-billing
  namespace: production
spec:
  name: billing
  owner: billing_app
  cluster:
    name: myapp-db
```

## Database with Extensions

Create database and install extensions:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata:
  name: myapp-db-billing
  namespace: production
spec:
  name: billing
  owner: billing_app
  cluster:
    name: myapp-db
---
# Extensions are managed separately or via initdb postInitSQL
```

## Multiple Databases on One Cluster

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata:
  name: cluster-billing-db
spec:
  name: billing
  owner: billing_app
  cluster:
    name: shared-cluster
---
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata:
  name: cluster-users-db
spec:
  name: users
  owner: users_app
  cluster:
    name: shared-cluster
---
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata:
  name: cluster-orders-db
spec:
  name: orders
  owner: orders_app
  cluster:
    name: shared-cluster
```

## Database Deletion Policy

Control what happens when the Database CR is deleted:

```yaml
spec:
  name: billing
  owner: billing_app
  cluster:
    name: myapp-db
  databaseReclaimPolicy: retain  # Keep database on CR deletion (default)
  # OR
  databaseReclaimPolicy: delete  # Drop database on CR deletion
```

## Deleting a Database

Option 1: Delete the CR (if reclaimPolicy is delete):
```bash
kubectl delete database myapp-db-billing
```

Option 2: Set ensure to absent:
```yaml
spec:
  name: billing
  owner: billing_app
  cluster:
    name: myapp-db
  ensure: absent
```

## Creating Owner Credentials

The owner role must exist. Create it via the Cluster bootstrap or manually:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: billing-app-credentials
  namespace: production
type: kubernetes.io/basic-auth
stringData:
  username: billing_app
  password: securepassword123
```

Then reference in Cluster bootstrap or create role manually.

## Full Workflow Example

```yaml
# 1. Cluster with app role
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: myapp-db
  namespace: production
spec:
  instances: 3
  storage:
    size: 20Gi
  bootstrap:
    initdb:
      database: app
      owner: app
      secret:
        name: app-db-credentials
---
# 2. Application credentials
apiVersion: v1
kind: Secret
metadata:
  name: app-db-credentials
  namespace: production
type: kubernetes.io/basic-auth
stringData:
  username: app
  password: generatedpassword123
---
# 3. Additional database for the same app
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata:
  name: myapp-db-analytics
  namespace: production
spec:
  name: analytics
  owner: app
  cluster:
    name: myapp-db
```

## Connection String for Application

```yaml
env:
- name: DATABASE_URL
  value: postgres://app:$(DB_PASSWORD)@myapp-db-rw:5432/billing
- name: DB_PASSWORD
  valueFrom:
    secretKeyRef:
      name: app-db-credentials
      key: password
```

## Notes

- Database CR must be in same namespace as Cluster
- Owner role must exist before creating Database
- Use `databaseReclaimPolicy: retain` for production safety
- Consider using one database per application for isolation
- The `-rw` service always points to the primary for writes
