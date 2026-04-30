# Primary Lease for CNPG — Design Notes (2026-04-29)

## Problem

When promoting a replica, before invoking `pg_promote`, CNPG waits for all WAL receivers to be down. The intent is to guarantee the primary has shut down before promotion proceeds.

However, if the promoted replica is **not in streaming mode**, it gets promoted immediately — potentially while the primary is still shutting down and archiving its WALs. This creates a risk of **data loss**.

Core question: how can we be certain the primary is truly down before allowing promotion?

## Proposal: Leader Lease

Let the primary acquire a **leader lease** (a distributed lock) and continuously renew it. Loss of the lease signals primary shutdown, giving replicas a reliable signal to act on.

The lease should be named after the cluster.

**RBAC:** the instance manager must be granted permission to work on the lease object.

---

## Behavior by Role

### Primary Instance

- Periodically renew the lease.
- If the lease renewal is lost → **shut down immediately**.
- If a different preferred holder is set → **shut down immediately**.
- Before graceful termination → **stop Postgres, then release the lease**.

### Replica

When designated as the **preferred holder**:

1. Start trying to grab the lock; wait until acquired.
2. Once acquired, promote and start renewing continuously.

Once active, the replica holds the lock forever until shut down. Entrypoints for the replica process:

- `start`
- wait until lock is held
- don't stop if the lock was not released
- stop immediately if not holding the lock

### Switchover

- Trigger a switchover by setting `targetPrimary` to the desired replica (existing mechanism). The current primary detects this and shuts down, allowing the target to acquire the lock and promote.

### Failover

- Is the primary pod not ready? → **Delete the pod.**
- Is the lock expired? → Choose a replica to elect and **set it as `targetPrimary`.**

---

## Instance Startup Logic

```
IS PG PRIMARY?
├── NO  → Free to start normally
└── YES → Check lease state
          ├── Lock held by me    → Adopt and renew it
          ├── Lock held by other → Demote myself
          └── Lock free          → Take it
```

---

## Open Questions / TODOs

- **Migration:** how do we handle existing clusters that have a current primary? Should we migrate them automatically?
- **Lock configuration defaults:** ~~need to find sensible defaults for lease TTL and renewal intervals.~~ **Resolved:** hardcode constants (e.g. `leaseDuration=15s`, `renewDeadline=10s`, `retryPeriod=2s`, mirroring Kubernetes leader-election defaults). Do not expose them in the API for now; add a tunable field later only if users hit real problems (e.g. high API-server latency across AZs).
- **Coordination complexity:** the interaction between the replica trying to grab the lock and the ongoing promotion is complex and needs careful design.

### Why `MaxStopDelay` is not the right value for lease TTL

`MaxStopDelay` and the lease TTL are orthogonal:

- **Graceful shutdown:** the primary keeps renewing the lease while Postgres winds down, then *explicitly releases* it before exiting (Step 4 in the checklist). The TTL is irrelevant in this path — the replica acquires the lease the instant it is released, regardless of how long the shutdown takes.
- **Crash / force-kill:** the primary stops renewing. The replica can only promote after the TTL expires. Setting TTL = `MaxStopDelay` (default 1800 s) would mean a 30-minute failover window after a crash — defeating the purpose of the mechanism.

`MaxStopDelay` bounds how long a graceful shutdown may take; it says nothing about how quickly a crash should be detected. Keep the TTL short (O(seconds)) for fast crash failover, and rely on explicit lease release for graceful shutdown.

## Side Effects

- **FACT:** with this mechanism in place, the isolation daemon can be **deprecated**.

---

## Implementation Plan

### Step 3 — Instance manager: startup decision tree

On startup, after determining the Postgres role:

- **Replica** → leave the runnable idle; Postgres starts normally.
- **Primary, lease held by me** → call `acquire()` to adopt and block until confirmed held; then start Postgres.
- **Primary, lease free** → call `acquire()` to take it and block until held; then start Postgres.
- **Primary, lease held by another pod** → do not call `acquire()`; demote Postgres and exit.

The lease is the gate: Postgres must never start as primary before the lease is held.

### Step 4 — Instance manager: graceful shutdown

Stop Postgres first, then release the lease. This ensures there is no window where a replica could promote while the primary is still serving writes. This is the active-state context-cancellation path from Step 2.

### Step 5 — Instance manager: replica promotion

When `targetPrimary` is set to this pod's name, call `acquire()` on the lease runnable and block until the lease is held before calling `pg_promote`. After promotion, the renewal loop from Step 2 continues running.

### Step 6 — Controller: currentPrimary sync

In the reconciliation loop, read the current holder of the `Lease` object and update `currentPrimary` accordingly. This keeps the existing API contract intact without breaking consumers.

### Step 7 — Controller: switchover

Set `targetPrimary` to the desired replica (existing mechanism). The `Lease` is a pure mutex — no changes to its fields are needed for signalling. Remove the now-redundant WAL-receiver-down wait.

### Step 8 — Controller: failover

Delete the non-ready primary pod to force lease expiry. Once the lease has expired, elect the best replica candidate (by LSN) and set it as `targetPrimary`.

### Step 9 — Migration

On operator startup, if no `Lease` exists for a cluster that has a known `currentPrimary`, create it with that primary as the current holder. This is transparent to users and requires no rolling restart.

### Step 10 — Deprecate isolation daemon

Add a deprecation notice to `IsolationCheckConfiguration` and emit a warning in operator logs. Do not remove in the same release.

### Step 11 — Tests

- **Unit:** lease runnable state machine, startup decision tree, idle/active termination behaviour.
- **E2E:** switchover via lease, failover via lease expiry, primary crash mid-WAL-archive (the original data-loss scenario), migration of a cluster with no existing lease.
