# Proof: at every moment in time, there could not be multiple running primary postmasters 

## PostgreSQL

### Definition (primary-postmaster)

A primary postmaster is a postmaster process that is not in recovery mode.

### Observation (promotion-asymmetry)

A replica can be promoted primary on the fly (with pg_promote) A primary cannot
be demoted on the fly and need a shut down

[check if it is really needed!!]

## Environment

### Axiom (write-consistency)

Writes to the API server are consistent. In particular, a write performed under
optimistic locking (i.e. one that does not bypass the resource's
`resourceVersion` check) must have observed the latest state of the resource at
the time it was accepted.

### Observation (read-staleness)

Reads from the API server can be stale, either because they are served from the
local state of an etcd replica, or because they are served from an informer's
local cache.

### Axiom (stale-read-monotonicity)

Stale reads are monotonic: a stale read can only return a state that was
actually observed at the resource at some point in the past. It can never return
a state that never existed, nor can it go backward and return a state older than
one already observed by the same reader.

This holds because etcd replicates state via Raft, which guarantees that every
replica's log is a prefix of the leader's log, and because Kubernetes
watches/informers deliver events to a given client in `resourceVersion` order
without gaps or reordering. The guarantee is scoped to a single client session:
if a client's connection is transparently re-established against a different
(and lagging) etcd member without carrying forward the last observed
`resourceVersion`, monotonicity for that client could in principle be violated.

We rely on client-go's `Reflector` to prevent this: on a watch disconnect it
always resumes (or relists) from the last observed `resourceVersion`, so
informer-backed reads never regress even across reconnects to a different API
server/etcd member.

## Demonstration

### Definition (lease-holding)

A client holds a Lease if it is recorded as `holderIdentity` in the Lease's
spec.

A challenger client B considers a Lease held by another identity A to be free to
take over only once B has observed the record's `(holderIdentity, renewTime)`
pair unchanged across successive local reads spanning a full `leaseDuration`,
that duration being measured by B's own clock. B never compares A's `renewTime`
against B's own clock, or against any wall-clock notion of "now" — it only
checks equality of the field across an interval it times itself. Consequently,
whether B concludes the Lease is free does not depend on clock synchronization
between A and B (it can still be affected by network partitions preventing B
from observing A's renewals, which is a liveness rather than a clock-skew
concern).

This is implemented by `tryTakeOver` and `sameHolder` in
`internal/cmd/manager/instance/run/lease/runnable.go:360-413`; see in particular
the comment at `runnable.go:351-354` ("The comparison is local and
equality-only, so clock skew between this pod and the previous holder cannot
trigger a take-over") and the observation-window check at `runnable.go:396-404`.
A cleanly released Lease (empty `holderIdentity`) is taken over immediately,
without waiting out this window (`runnable.go:387-389`).

### Lemma (lease-mutual-exclusion): there can't be two concurrent holders of a Lease object

Claiming a Lease (becoming its `holderIdentity`, or renewing an existing hold)
requires a write to the Lease object, performed under optimistic locking. By
Axiom (write-consistency), such a write is only accepted if it observed the
latest state of the object, i.e. its `resourceVersion` still matches what the
API server holds at the time the write is evaluated.

Suppose a client A currently holds the Lease per Definition (lease-holding), and
is still renewing it. A second client B, having concluded per Definition
(lease-holding) that the Lease is free (correctly or not — e.g. because of a
network partition hiding A's renewals from B), attempts to claim it. B's write
is a conditional update based on the `resourceVersion` it last observed. If A
renews at any point up to the moment B's write is evaluated by the API server,
A's renewal has already advanced the `resourceVersion`, so B's write no longer
matches the latest state and is rejected as a conflict. B can only succeed if A
has stopped renewing before B's write is evaluated, in which case A is, by
Definition (lease-holding), no longer a holder by the time B becomes one.

Two clients can therefore never both hold the Lease at the same time: whichever
write reaches the API server second against a given `resourceVersion` is
rejected, so at most one claim can succeed from any given state of the object.

Note that this argument establishes safety (no concurrent holders) without
relying on any bound on clock skew. A network partition, or a lease duration too
short relative to renewal latency, can still cause B to *attempt* a takeover
while A is healthy and rightfully holding the Lease (a liveness/soundness
concern — an unnecessary or premature failover) — but it cannot cause both A and
B to hold the Lease simultaneously, since that outcome is precluded by Axiom
(write-consistency) regardless of why B attempted the takeover.

This argument assumes B's takeover is performed as a conditional write against
`resourceVersion` (per Axiom (write-consistency)); a write that bypasses
optimistic locking (e.g. an unconditional overwrite) is not covered by this
lemma.

#### Code references

This lemma was checked against the primary-lease implementation in
`internal/cmd/manager/instance/run/lease/runnable.go` and the vendored
`k8s.io/client-go` (`v0.36.2`) `leaderelection`/`resourcelock` packages it
builds on:

- `(*Runnable) claim`, `runnable.go:419-432`: writes the new holder via
  `r.lock.Update`, using a record derived from a preceding `Get` (the code
  comment at `:416-418` states this is a ResourceVersion-conditional write and
  that a losing competitor "makes it fail with a conflict"). This is the write
  path that Axiom (write-consistency) and this lemma's proof rely on.
- `(*Runnable) tryTakeOver`, `runnable.go:360-406`, and `sameHolder`,
  `runnable.go:411-413`: the take-over decision logic underlying Definition
  (lease-holding), including the bounded `Get` (`runnable.go:365-367`) and the
  local observation window (`runnable.go:396-404`).
- `resourcelock.LeaseLock.Update`,
  `k8s.io/client-go/tools/leaderelection/resourcelock/leaselock.go:74-88`:
  confirms the `Update` is a typed-client PUT carrying the `resourceVersion`
  observed at the last `Get`/ `Create`, i.e. standard Kubernetes optimistic
  concurrency — not a `Patch` and not an unconditional overwrite.
- `leaderelection.tryAcquireOrRenew`,
  `k8s.io/client-go/tools/leaderelection/leaderelection.go:432-503`: the renewal
  fast path (`:444-455`) and slow path (`:458-499`) both go through the same
  conditional `LeaseLock.Update`, so ongoing renewals by a healthy holder A
  enjoy the same guarantee as the initial `claim`.
- Note: client-go's own take-over decision (`isLeaseValid`, referenced at
  `leaderelection.go:444,480`) *does* compare the Lease's
  `renewTime`/`leaseDurationSeconds` against local wall-clock time, which would
  reintroduce a clock-skew dependency. This path is moot in this fork because
  `preAcquire` (`runnable.go:452-484`) always establishes this pod as holder
  before handing off to `leaderelection.NewLeaderElector`
  (`runnable.go:541-563`, see comment at `:550-552`): the elector is only ever
  used for renewal of a lease we already hold, never to decide a take-over. The
  clock-skew-free property of this lemma therefore depends specifically on
  `preAcquire`/`tryTakeOver` gating every acquisition, not on client-go's
  `leaderelection` defaults alone.

### Lemma (no-concurrent-primaries-while-renewing)

**While the primary lease holder keeps renewing before `renewDeadline`, it is
not possible to have multiple running primary postmasters.**

Failing to renew in time (for any reason: unreachability, API server latency,
control-loop starvation, ...) is out of scope here and needs its own lemma:
losing the lease does not, by itself, stop PostgreSQL, so a primary that fails
to renew in time can keep running as a live primary postmaster until some other
mechanism (fencing, step-down, a watchdog) actually acts on it.

A primary postmaster can start on a primary PGDATA on which the instance manager
started, or on a replica PGDATA that the instance manager promoted. So we cover
that in two separate cases.

#### Case (already-primary-start): instance manager starts on an already-primary PGDATA

This covers both a cluster's first-ever start and a restart (e.g. after a crash
or pod reschedule) of a PGDATA that is already primary: in both cases the
instance manager starts PostgreSQL directly, without a promotion.

The instance manager acquires the lease before starting PostgreSQL. By Lemma
(lease-mutual-exclusion), only one of them will be able to grab the lock.

##### Code references

Lease acquisition for this case is folded into `initialize()`
(`internal/management/controller/instance_controller.go:529`) via
`verifyPgDataCoherenceForPrimary`
(`internal/management/controller/instance_startup.go:43`), specifically its
`targetPrimary == r.instance.GetPodName()` branch (the case where this pod's
PGDATA is already primary and this pod is also the designated target primary —
i.e. PostgreSQL is about to start directly as a live primary, with no promotion
step to gate on). `initialize()` runs once, on the first reconcile, strictly
before `systemInitialization.Broadcast()` (`instance_controller.go:227`), which
is what `postgresLifecycleManager` waits on before starting PostgreSQL
(`cmd.go:244-350`): blocking here therefore blocks PostgreSQL's start itself.
The shared `acquirePrimaryLease` helper (`instance_controller.go`) is used by
both this case and Case (promoted-replica) below. A lease-acquisition failure
returns `controller.ErrNextLoop` (`instance_startup.go`), which
`handleErrNextLoop` (`instance_controller.go:521`) turns into a clean 1-second
requeue of the whole `Reconcile` (and thus of `initialize()`, since
`firstReconcileDone` is only latched on success).

This was checked against the code along five angles:

1. PostgreSQL's only exec call site, `instance.Run()`
   (`internal/cmd/manager/instance/run/lifecycle/run.go:103`), is reached solely
   through `runPostgresAndWait`, which unconditionally calls
   `i.systemInitialization.Wait()` (`run.go:78`) immediately before it. This
   single loop (`lifecycle.go:78-187`) covers the initial start, a
   crash-restart, and any command-triggered restart — there is no second exec
   path. `restartPrimaryInplaceIfRequested` (`instance_controller.go:392`) and
   the online-upgrade flag `InstanceManagerIsUpgrading` only ever act on an
   already-running postmaster that already passed this gate; neither re-execs
   `Run()` independently.
2. The `targetPrimary == r.instance.GetPodName()` branch is confirmed reachable
   only once `IsPrimary()` (`instance_startup.go:44-50`) has already returned
   true earlier in the same function — i.e. this is exactly the "already
   primary, designated target" case, and `acquirePrimaryLease` runs before the
   pre-existing `currentPrimary == ""` bootstrap logic in that branch.
3. `Runnable.Acquire` (`lease/runnable.go:182-199`) returns nil only via `case
   <-r.heldCh`, and `heldCh` is closed exactly once (`runnable.go:538`), on the
   successful-acquisition path inside `preAcquire`; a timeout/cancellation
   returns `ctx.Err()` (`context.DeadlineExceeded`), never nil. There is no path
   for `acquirePrimaryLease` to return success without the lease actually held.
4. `leaseRunnable` (`cmd.go:255`) is constructed and wired in unconditionally,
   with no feature gate, cluster-spec field, or empty-`Config` no-op path found
   that could route around `Acquire`.
5. No other production call site starts or restarts PostgreSQL outside the
   `systemInitialization`-gated loop in `lifecycle.go`.

Every path that can exec the postmaster on an already-primary PGDATA passes
through `systemInitialization.Wait()`, which is only broadcast after
`acquirePrimaryLease` succeeds.

#### Case (promoted-replica): promoted replica

The instance manager acquires the lease before invoking pg_promote.

##### Code references

In `reconcilePrimary`
(`internal/management/controller/instance_controller.go:1274-1314`),
`acquirePrimaryLease` (line 1281) runs first and returns early on error/timeout;
only once it succeeds does the code check `IsPrimary()` (line 1293) and, if
false, call `handlePromotion` (line 1311), which calls `instance.PromoteAndWait`
(`pkg/management/postgres/promote.go:35-94`) — the Go wrapper that runs `pg_ctl
-D <PGDATA> -w promote`. `PromoteAndWait` has exactly one caller in non-test
code (`instance_controller.go:1361`); the `kubectl cnpg promote` CLI plugin
(`internal/cmd/plugin/promote/promote.go:36`) only patches
`cluster.Status.TargetPrimary`, re-entering this same lease-then-promote path
rather than calling PostgreSQL directly. No other call site of `PromoteAndWait`,
webserver endpoint, or signal-file path was found.

There is no lease re-check between `acquirePrimaryLease` succeeding and the
actual `pg_ctl promote` call — the only check in between
(`verifyPromotionToken`) is unrelated (replica-cluster promotion-token gating).
This is consistent with this lemma's hypothesis: while the lease holder keeps
renewing, no such loss can occur in that window; a hypothetical loss occurring
there is exactly the scenario Lemma (primary-fenced-on-lease-loss) needs to
cover.

### Lemma (primary-fenced-on-lease-loss)

By Lemma (lease-mutual-exclusion), there is only ever a single instance manager
renewing the primary lease at a time, so it is enough to reason about that one
holder's fate. It stops renewing either because it chose to (voluntary release)
or because it failed to (involuntary loss); we cover these in two separate
cases.

This lemma assumes no manual intervention that bypasses these automated
mechanisms — e.g. someone manually deleting or hand-editing the Lease object,
forcing `cluster.Status.TargetPrimary` directly, or disabling the isolation
check (`IsolationCheckConfiguration.Enabled = false`). Such actions are out of
scope: this lemma only covers what the system does on its own.

#### Case (voluntary-release): voluntary release

This is triggered either by something telling the instance manager to shut down
(the controller manager's context being cancelled, or a termination signal from
the kubelet), or by a switchover: the reconcile loop noticing this pod is no
longer `cluster.Status.TargetPrimary` and asking this old primary to step down.
In every case PostgreSQL is shut down first; only once that shutdown attempt has
completed does the instance manager release the lease; only after that does the
process itself terminate. So there is no point at which the lease is free while
this pod's postmaster could still be running as primary.

##### Code references

`PostgresLifecycle.Start`
(`internal/cmd/manager/instance/run/lifecycle/lifecycle.go:68-188`) handles the
first two triggers directly in its `select` loop: on `ctx.Done()` (line 122) or
on a `SIGINT`/`SIGTERM` signal (line 137), it calls
`instance.TryShuttingDownSmartFast(ctx)` and only then `return`s.

The switchover trigger goes through a different path that converges on the same
shutdown-then-release ordering: `reconcileOldPrimary`
(`internal/management/controller/instance_controller.go:618-644`) calls
`instance.RequestFastImmediateShutdown()` (line 638), which is handled by the
same `PostgresLifecycle.Start` loop's command-channel branch
(`lifecycle.go:162-181`) via `HandleInstanceCommandRequests` —
`shutDownFastImmediate` (`pkg/management/postgres/instance.go:1701-1705`) shuts
PostgreSQL down and returns `restartNeeded = false`, so the loop does not
restart the postmaster; the subsequent postmaster-exit event instead causes
`Start` to `return` (`lifecycle.go:116-120`), which runs the deferred
`i.globalCancel()` (`lifecycle.go:75`) and cancels the shared context that
`mgr.Start` is running under.

In all three cases, since `PostgresLifecycle` is a controller-runtime Runnable,
`mgr.Start` (`cmd.go:445`) does not itself return until every Runnable it
manages has returned — so it does not return until the shutdown attempt has
completed. Only after `mgr.Start` returns does the deferred call to
`leaseRunnable.Release` (`cmd.go:386-393`) run; the process terminates only
after that defer (and the rest of `runSubCommand`) returns. `Release`'s own doc
comment (`runnable.go:206-208`) states this explicitly: "controller-runtime has
already waited for all runnables (including PostgresLifecycle) to finish, so
PostgreSQL is down and releasing the lease lets a replica promote at once."

The one exception is an in-place instance-manager upgrade
(`lifecycle.go:126-130`, `runnable.go:210-222`): there, PostgreSQL is
deliberately kept running and the lease is not released at all — the replacement
process reuses the same pod name (`Identity`) and re-adopts the lease with no
free-lease window, so this exception does not open a gap either.

#### Case (involuntary-loss): involuntary loss

##### Sub-case (api-unreachable)

The instance manager is otherwise healthy and its renewal loop is running, but
it cannot reach (or gets errors from) the API server, so it must self-fence
without relying on it.

This condition is detected once the lease becomes unverifiable: `RenewDeadline`
has elapsed without a successful renewal, and the confirmation `Get` that
follows also fails (a renewal miss that a subsequent `Get` *can* confirm is just
a transient blip, and does not trigger any of what follows). Detection itself is
bounded: both that confirmation `Get` and the `Get` in the `preAcquire` retry
that follows are individually bounded by `RetryPeriod`, so detection takes at
most `RenewDeadline + 2*RetryPeriod` in the worst case (both timing out) before
`shouldStepDown` is even called; `shouldStepDown` then decides between two
sub-sub-cases by asking every peer's `/failsafe` endpoint whether it still
agrees we are the target primary.

If a peer is unreachable, or a reachable peer names a different target primary,
the step-down verdict is met, and we request an immediate shutdown: we won't
have two primaries as long as that shutdown completes within `leaseDuration`
minus `RenewDeadline + 2*RetryPeriod` minus however long the peer pings
themselves take (bounded per peer, but run sequentially over all peers with no
overall deadline).

If every peer is reachable and still agrees we are the target primary, the
step-down verdict is not met, and we keep retrying without shutting down. This
state is self-limiting rather than an open gap: since the Lease still names us
as `holderIdentity` (it was never gracefully released), any challenger must
observe it unchanged for a full `leaseDuration` of its *own* polling before it
may claim it (`tryTakeOver`'s `observedTime` logic), regardless of how stale the
record already is in wall-clock terms. But the moment the operator promotes a
peer, that peer's own `/failsafe` answer flips to itself immediately — before it
even starts that `leaseDuration`-long wait, let alone finishes it — because
`/failsafe` just reads the peer's own already-updated cached `Cluster` object.
Since we re-run `shouldStepDown` every `RetryPeriod` for as long as we stay in
this branch, we detect that peer's disagreement (and start shutting down) within
about one `RetryPeriod` of its cache updating — i.e. with a margin of nearly a
full `leaseDuration` before that peer could possibly complete its takeover. So
"every peer agrees" cannot remain true for the whole duration of a real
promotion; it necessarily flips to the met case above well before a new primary
could take the lease.

The one residual case this doesn't cover: if our own cached peer roster
(`cluster.Status.InstancesReportedState`, frozen since we lost our own watch)
never included an entry for the promoted peer at all, we could never ping it and
would never observe its disagreement through that peer directly.

This needs the peer to have never been in our roster to begin with, not merely
to have gone quiet: if an *already-known* peer dies and is recreated, our next
ping to its known address simply fails, which `shouldStepDown` treats the same
as any other unreachable peer (`stepDown=true`) — so losing a previously-known
peer, even temporarily, self-fences us regardless. The residual case therefore
needs a genuinely new instance, one that came into existence only after our
roster froze — e.g. a user increasing `spec.instances` during our isolation
window (an operator-initiated replacement of an already-tracked, now-dead
replica doesn't qualify, since that peer's disappearance is itself caught by the
check above). And even then, in a cluster with more than two instances, any
other pre-existing peer would likely reveal the same `TargetPrimary` change
instead — but only if that other peer can itself still reach the API server: if
the outage is broader than a single isolated pod (e.g. the API server itself is
degraded for everyone, not just us), no peer's cache would be updating either,
and this fallback would not apply.

###### Code references

This sub-case was checked against
`internal/cmd/manager/instance/run/lease/runnable.go` and `isolation.go` along
the whole detection-to-shutdown chain:

- `le.Run(ctx)` (`runnable.go:568`), configured with `RenewDeadline`
  (`runnable.go:544`), gives up renewing after `RenewDeadline` and returns —
  ordinary client-go `leaderelection` behavior, not reimplemented here.
- The post-run confirmation `Get` (`runnable.go:578-580`) is bounded by
  `context.WithTimeout(ctx, r.config.RetryPeriod)`. `classifyLeaseAfterRun`
  (`runnable.go:296-313`) returns `leaseUnverifiable` when this `Get` itself
  fails; that case (`runnable.go:588-593`) just logs and loops back into
  `preAcquire` — it does not call the step-down check directly.
- `checkStepDownOnFailure` is threaded as `acquiredBefore`
  (`runnable.go:528-534`): `false` before the first successful acquisition, set
  `true` unconditionally right after and on every loop iteration from then on —
  so it stays `true` for every subsequent `preAcquire` call, not just the first
  retry after losing the lease.
- Inside `preAcquire`'s retry loop (`runnable.go:458-483`), `tryTakeOver`'s own
  `Get` (`runnable.go:365-367`) is likewise bounded by `context.WithTimeout(ctx,
  RetryPeriod)`. On failure, with `checkStepDownOnFailure` true,
  `evaluateStepDownOnLeaseFailure` is called from inside the loop body
  (`runnable.go:473-475`) — i.e. on *every* failed iteration, not once, as the
  comment at `runnable.go:442-451` also states.
- `shouldStepDown` (`isolation.go:176-198`) returns `(false, nil)` outright if
  the isolation check is disabled or `cluster.Spec.Instances == 1`
  (`isolation.go:181-186`). Otherwise `ensureInstancesAreReachable`
  (`isolation.go:110-124`) pings every peer's `/failsafe` endpoint; each ping is
  bounded by the isolation check's own `ConnectionTimeout`/`RequestTimeout`
  (`isolation.go:68,75`), but the loop over all peers has no overall deadline. A
  ping failure yields `PingError`; a reachable peer naming a different target
  yields `SupersededError`; either makes `shouldStepDown` return `(true, err)`
  (`isolation.go:114-119`). If every peer is reachable and agrees, it returns
  `(false, nil)` (`isolation.go:193-197`).
- `evaluateStepDownOnLeaseFailure` (`runnable.go:331-342`) calls
  `instance.RequestImmediateShutdown()` exactly in the `stepDown == true`
  branch; when `stepDown` is `false` (with or without a transient error), it
  only logs and lets the caller retry — confirming the polarity used above.

##### Sub-case (renewal-loop-stuck)

The instance manager's own process is wedged and cannot even attempt a renewal
or reason about stepping down, so fencing must come from an external actor
instead; that actor sits at one of three layers, each slower and less certain
than the last.

###### Sub-sub-case (instance-manager-stuck)

Only the instance manager process itself is wedged, so the liveness handler —
running in a separate goroutine — both fails the probe and makes a best-effort
attempt to request an immediate PostgreSQL shutdown itself, so this does not
have to wait for the kubelet to kill the container. The watchdog detects that
the renewal loop has stopped attempting work at all, as opposed to attempting it
and failing (the `api-unreachable` sub-case above) — it needs no knowledge of
*why* the loop stopped, only that it did.

**Verification:** `LeaseWatchdog`
(`internal/management/watchdog/leasewatchdog.go`) wraps the lease's
`resourcelock.Interface` (`WrapLock`, lines 85-119) so every
`Get`/`Update`/`Create` *attempt* — regardless of success or failure — calls
`Beat()` (lines 53-55) first, stamping the current time; `IsHealthy()` (lines
66-78) compares elapsed time since that stamp against `maxSilence`, only once
`MarkAcquired()` (lines 62-64) has been called, so a replica or a first-time
contender is never fenced by it. The liveness handler
(`pkg/management/postgres/webserver/probes/liveness.go:79-84`) checks this
first, synchronously, in the same HTTP request that will report the failure, and
calls `instance.TryRequestImmediateShutdown()`
(`pkg/management/postgres/instance.go:1549-1556`) before writing the 500. That
method's `select { case instanceCommandChan <- shutDownImmediate: ...; default:
... }` cannot block — a send-select with only one case plus `default` resolves
immediately either way — so a liveness handler calling it is never itself stuck
waiting on a lifecycle manager that turns out not to be listening. When
received, `PostgresLifecycle`'s command loop
(`internal/cmd/manager/instance/run/lifecycle/lifecycle.go:162-171`) runs
`TryShuttingDownImmediate` (`pkg/management/postgres/instance.go:740-761`),
which executes `pg_ctl stop -m immediate -t <timeout>`, `timeout` being
`GetImmediateShutdownTimeout()` (default 30s, `api/v1/cluster_types.go:1485`,
`cluster_funcs.go:815-820`).

Computing the worst case with this fix in place, assuming (as this
sub-sub-case's hypothesis requires) that only the lease-renewal goroutine is
wedged and the HTTP server and `PostgresLifecycle` goroutines are both healthy:
`maxSilence` (10s default) + up to one more liveness `PeriodSeconds` (10s
default) waiting for the next probe to fire + near-zero channel delivery (the
command loop's `select` is normally idle and ready) + up to
`immediateShutdownTimeout` (30s default) for `pg_ctl` to actually report success
— **≈50s worst case**, which does *not* fit under the default 15s
`leaseDuration`; only the common case (a near-instant `pg_ctl` stop, not the
full 30s timeout) lands comfortably inside it. The fix meaningfully shortens the
typical path (bypassing the kubelet's `TerminationGracePeriodSeconds`, which
defaults to a much larger 1800s via `cluster.Spec.MaxStopDelay`, entirely in the
common case), but does not by itself guarantee the bound in the true worst case
— that still depends on configuration, per the formula below.

**Sizing formula (advice for operators):** neither the primary-lease webhook
(`internal/webhook/v1/cluster_webhook.go`, `validatePrimaryLease`) nor any other
validation cross-checks `leaseDuration` against the liveness-probe or
shutdown-timeout settings, so nothing prevents an inconsistent configuration
from passing admission. For this sub-sub-case's fast path (this fix) to be
guaranteed safe rather than merely typical-case-safe, size the cluster so that:

```
maxSilence + livenessProbePeriodSeconds + immediateShutdownTimeoutSeconds
  < leaseDurationSeconds
```

As a fallback for when the non-blocking send isn't delivered, the older
kubelet-mediated path should also be kept tight:

```
maxSilence + livenessProbePeriodSeconds
  + (livenessFailureThreshold - 1) * livenessProbePeriodSeconds
  + terminationGracePeriodSeconds
  < leaseDurationSeconds
```

The pod's own `terminationGracePeriodSeconds` comes from
`cluster.Spec.MaxStopDelay` and also bounds *ordinary* graceful shutdowns
elsewhere (e.g. during a planned restart), so it shouldn't be shrunk purely for
this formula. But `corev1.Probe` carries its own, separate
`TerminationGracePeriodSeconds` field (`k8s.io/api/core/v1/types.go:3782`),
which overrides the grace period used specifically when *this probe's* failure
is what triggers the kill — CNPG does not currently set it on the LivenessProbe
(`pkg/specs/pods.go` only sets the pod-level field, at line 205, from
`MaxStopDelay`). Setting a tight, probe-specific
`LivenessProbe.TerminationGracePeriodSeconds` would let this fallback formula be
satisfied without touching `MaxStopDelay` or affecting graceful shutdowns
elsewhere at all.

One caveat that limits both formulas today: `maxSilence` is currently hardcoded
to `5 * defaultRetryPeriod` (`runnable.go:68`, always 10s) in `New()`
(`runnable.go:144`), which runs before `Acquire` ever sees the cluster's actual
configured `RetryPeriod` — so `maxSilence` does not currently shrink even if an
operator tightens `RetryPeriodSeconds` to make `leaseDuration` itself smaller.
Treat `maxSilence` as a fixed 10s floor in the formulas above unless this is
also changed to derive from the configured `RetryPeriod`.

###### Sub-sub-case (kubelet-stuck)

Here "stuck" means *only* the kubelet is unresponsive — the instance manager
process and its renewal loop are healthy, and the API server is reachable
(otherwise we would be in `instance-manager-stuck` above, or in Sub-case
(api-unreachable)). Under that premise, the primary lease keeps renewing
normally: nothing about renewing the lease involves the kubelet at all, only the
instance manager and the API server. So this is not actually an involuntary-loss
scenario in the first place — the lease is never lost, and Lemma
(lease-mutual-exclusion) alone already guarantees no challenger can succeed
while renewal keeps happening, regardless of whether the kubelet could act on a
liveness probe or not. A stuck kubelet only matters as a *missing backstop* for
some other failure (e.g. if the renewal loop were *also* stuck, which is the
`instance-manager-stuck` case above, or if the whole node froze, which is
`node-stuck` below) — on its own, it poses no danger to this lemma.

###### Sub-sub-case (node-stuck)

Here "stuck" means the node itself is genuinely frozen (hung kernel, hypervisor
stall, hardware fault) — not merely disconnected from the control plane while
otherwise executing normally. That distinction matters: a node that's only
network-partitioned from the API server, but still scheduling processes and
serving client connections fine, is not this sub-sub-case at all — its instance
manager's renewal loop would still be running (attempting and failing, not
wedged), which is Sub-case (api-unreachable) above, with `shouldStepDown`'s
peer-reachability check as the relevant safety mechanism.

Given a truly frozen node, the postmaster runs on that same node and shares
whatever is freezing everything else on it (CPU scheduling, disk I/O, the
network stack) — so it is very likely just as unable to make progress as the
instance manager's own renewal-loop goroutine is. It may still exist as a
process, technically satisfying Definition (primary-postmaster) (not in recovery
mode), but a postmaster that cannot schedule work, read or write to disk, or
accept client connections cannot actually commit any new transaction. The
theorem's real concern — two primaries independently diverging by committing
conflicting writes — does not materialize here: a frozen node's postmaster is
functionally inert even while nominally "running," so Kubernetes' inability to
guarantee its termination (the gap this sub-sub-case's own formula couldn't
close) does not by itself lead to data divergence.

This does not by itself prove *no* observable divergence, though — a client
already holding an open connection when the node froze could have a transaction
that already committed locally (fsynced) but whose acknowledgment never reached
the client, which is an ordinary crash-recovery ambiguity already present in
single-primary PostgreSQL and not specific to this lemma.

## Conclusion

Together, Lemma (no-concurrent-primaries-while-renewing) and Lemma
(primary-fenced-on-lease-loss) give the demonstration of the theorem stated at
the top of this document. At every moment, the current primary lease holder is
in exactly one of two states: still successfully renewing, or having stopped
(voluntarily or involuntarily) — these two lemmas partition that alternative
exhaustively. The first covers safety while nothing has gone wrong; the second
covers what happens once renewal stops, case by case, down to the specific
mechanism (or, where identified, the residual gap or required configuration)
that keeps a stopped holder from overlapping with whoever takes the lease next.
