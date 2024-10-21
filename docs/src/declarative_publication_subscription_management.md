# Declarative Publication/Subscription Management

Declarative publication/subscription management enables users to set up
logical replication via the following Custom Resource Definitions (CRD):

- `Database` ,
- `Publication`,
- `Subscription`,

The Database CRD is discussed in depth in the
["Declarative database management"](declarative_database_management.md) section.
In this section we describe `Publication` and `Subscription` in more detail.

## Overview

The procedure to set up logical replication:

- Begins with two CloudNativePG clusters.
    - One of them will be the "source"
    - The "destination" cluster should have an `externalClusters` stanza
      containing the connection information to the source cluster
- A Database object creating a database (e.g. named `sample`) in the source
  cluster
- A Database object creating a database with the same name in the destination
  cluster
- A Publication in the source cluster referencing the database
- A Subscription in the destination cluster, referencing the Publication that
  was created in the previous step

Once these objects are reconciled, PostgreSQL will replicate the data from
the source cluster to the destination cluster using logical replication. There
are many use cases for logical replication; please refer to the
[PostgreSQL documentation](https://www.postgresql.org/docs/current/logical-replication.html)
for detailed discussion.

!!! Note
    the `externalClusters` section in the destination cluster has the same
    structure used in [database import](database_import.md) as well as for
    replica clusters. However, the destination cluster does not necessarily
    have to be bootstrapped via replication nor import.

### Example: Simple Publication Declaration

A `Publication` object is managed by the instance manager of the source
cluster's primary instance.
Below is an example of a basic `Publication` configuration:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Publication
metadata:
  name: pub-one
spec:
  name: pub
  dbname: cat
  cluster:
    name: source-cluster
  target:
    allTables: true
```

The `dbname` field specifies the database the publication is applied to.
Once the reconciliation cycle is completed successfully, the `Publication`
status will show a `ready` field set to `true`, and an empty `error` field.

### Publication Deletion and Reclaim Policies

A finalizer named `cnpg.io/deletePublication` is automatically added
to each `Publication` object to control its deletion process.

By default, the `publicationReclaimPolicy` is set to `retain`, which means
that if the `Publication` object is deleted, the actual PostgreSQL publication
is retained for manual management by an administrator.

Alternatively, if the `publicationReclaimPolicy` is set to `delete`,
the PostgreSQL publication will be automatically deleted when the `Publication`
object is removed.

### Example: Publication with Delete Reclaim Policy

The following example illustrates a `Publication` object with a `delete`
reclaim policy:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Publication
metadata:
  name: pub-one
spec:
  name: pub
  dbname: cat
  publicationReclaimPolicy: delete
  cluster:
    name: source-cluster
  target:
    allTables: true
```

In this case, when the `Publication` object is deleted, the corresponding PostgreSQL publication will also be removed automatically.

### Example: Simple Subscription Declaration

A `Subscription` object is managed by the instance manager of the destination
cluster's primary instance.
Below is an example of a basic `Subscription` configuration:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Subscription
metadata:
  name: sub-one
spec:
  name: sub
  dbname: cat
  publicationName: pub
  cluster:
    name: destination-cluster
  externalClusterName: source-cluster
```

The `dbname` field specifies the database the publication is applied to.
The `publicationName` field specifies the name of the publication the subscription refers to.
The `externalClusterName` field specifies the external cluster the publication belongs to.

Once the reconciliation cycle is completed successfully, the `Subscription`
status will show a `ready` field set to `true` and an empty `error` field.

## Subscription Deletion and Reclaim Policies

A finalizer named `cnpg.io/deleteSubscription` is automatically added
to each `Subscription` object to control its deletion process.

By default, the `subscriptionReclaimPolicy` is set to `retain`, which means
that if the `Subscription` object is deleted, the actual PostgreSQL publication
is retained for manual management by an administrator.

Alternatively, if the `subscriptionReclaimPolicy` is set to `delete`,
the PostgreSQL publication will be automatically deleted when the `Publication`
object is removed.

### Example: Subscription with Delete Reclaim Policy

The following example illustrates a `Subscription` object with a `delete`
reclaim policy:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Subscription
metadata:
  name: sub-one
spec:
  name: sub
  dbname: cat
  publicationName: pub
  subscriptionReclaimPolicy: delete
  cluster:
    name: destination-cluster
  externalClusterName: source-cluster
```

In this case, when the `Subscription` object is deleted, the corresponding PostgreSQL publication will also be removed automatically.
