/*
Copyright © contributors to CloudNativePG, established as
CloudNativePG a Series of LF Projects, LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

SPDX-License-Identifier: Apache-2.0
*/

package lease

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/cloudnative-pg/cloudnative-pg/pkg/management/postgres"
)

const (
	// leaseDuration is how long a lease is valid before it expires.
	leaseDuration = 15 * time.Second

	// renewDeadline is how long the holder tries to renew before giving up.
	renewDeadline = 10 * time.Second

	// retryPeriod is how frequently a non-holder retries acquiring the lease.
	retryPeriod = 2 * time.Second

	// releasedLeaseDurationSeconds is the TTL written when explicitly releasing
	// the lease. An empty HolderIdentity already marks the lease as free, but
	// setting the duration to 1 second mirrors the k8s leaderelection.release()
	// behaviour and acts as belt-and-suspenders: even if an acquirer does not
	// treat an empty identity as immediately available, it waits at most one
	// second before the TTL expires and it can take the lease.
	releasedLeaseDurationSeconds = 1
)

// Runnable manages the primary lease for this instance.
// It starts idle and enters the acquisition/renewal loop only after Acquire is called.
type Runnable struct {
	instance *postgres.Instance
	lock     *resourcelock.LeaseLock

	// activateCh is closed by the first Acquire call to unblock Start.
	activateCh   chan struct{}
	activateOnce sync.Once

	// heldCh is closed once the lease has been successfully acquired for the first time.
	heldCh   chan struct{}
	heldOnce sync.Once

	// cancelOnLoss is called when the lease is lost without the Start context being
	// cancelled first, signalling that the instance manager must stop immediately.
	cancelOnLoss context.CancelFunc
}

// New creates a new Runnable.
// cancelOnLoss should cancel the context that governs the whole instance manager
// (i.e. onlineUpgradeCancelFunc); it is invoked when the lease is lost unexpectedly.
func New(
	kubeClient kubernetes.Interface,
	instance *postgres.Instance,
	cancelOnLoss context.CancelFunc,
) *Runnable {
	return &Runnable{
		instance: instance,
		lock: &resourcelock.LeaseLock{
			LeaseMeta: metav1.ObjectMeta{
				Namespace: instance.GetNamespaceName(),
				Name:      instance.GetClusterName(),
			},
			Client: kubeClient.CoordinationV1(),
			LockConfig: resourcelock.ResourceLockConfig{
				Identity: instance.GetPodName(),
			},
		},
		activateCh:   make(chan struct{}),
		heldCh:       make(chan struct{}),
		cancelOnLoss: cancelOnLoss,
	}
}

// Acquire signals the runnable to start competing for the lease,
// then blocks until the lease is held or ctx is cancelled.
func (r *Runnable) Acquire(ctx context.Context) error {
	r.activateOnce.Do(func() { close(r.activateCh) })
	select {
	case <-r.heldCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release explicitly releases the lease so that a replica can promote without
// waiting for the TTL to expire. It is a no-op if the lease was never held.
// Callers must pass a fresh (non-cancelled) context; the previous run context
// is already cancelled by the time this is called.
// TODO(Step 4): block here until Postgres is truly down before releasing.
// postgres.Instance has no public "wait for postmaster to exit" API yet — see
// the note in Step 4 of the implementation plan.
func (r *Runnable) Release(ctx context.Context) error {
	select {
	case <-r.heldCh:
		// we held the lease at some point — proceed with release
	default:
		return nil
	}

	return r.lock.Update(ctx, resourcelock.LeaderElectionRecord{
		LeaseDurationSeconds: releasedLeaseDurationSeconds,
	})
}

// Start implements controller-runtime's Runnable interface.
// The runnable stays idle until Acquire is called, then enters the
// lease acquisition/renewal loop.
func (r *Runnable) Start(ctx context.Context) error {
	select {
	case <-r.activateCh:
		// proceed to active mode
	case <-ctx.Done():
		return nil
	}
	return r.runLeaderElection(ctx)
}

func (r *Runnable) runLeaderElection(ctx context.Context) error {
	contextLogger := log.FromContext(ctx).WithName("primary-lease")

	// becameLeader is set to true the moment we first acquire the lease.
	// It is stored from the OnStartedLeading goroutine and read from OnStoppedLeading
	// which fires after le.renew returns, well after the first renewal cycle — so the
	// write always happens before the read in practice.
	var becameLeader atomic.Bool

	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          r.lock,
		LeaseDuration: leaseDuration,
		RenewDeadline: renewDeadline,
		RetryPeriod:   retryPeriod,
		// ReleaseOnCancel is false because the context passed to le.Run is the shared
		// onlineUpgradeCtx: it is cancelled concurrently with PostgresLifecycle shutting
		// down Postgres, so Postgres may not have stopped yet when the release would fire.
		// In Step 4 we will give le.Run its own inner context, stop Postgres first, and
		// only then cancel that context — at which point ReleaseOnCancel: true becomes
		// correct and the Release method below can be removed.
		ReleaseOnCancel: false,
		Name:            r.instance.GetClusterName(),
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(context.Context) {
				becameLeader.Store(true)
				r.heldOnce.Do(func() { close(r.heldCh) })
			},
			OnStoppedLeading: func() {
				if becameLeader.Load() && ctx.Err() == nil {
					contextLogger.Warning("Primary lease lost unexpectedly, shutting down instance manager")
					r.cancelOnLoss()
				}
			},
			OnNewLeader: func(string) {},
		},
	})
	if err != nil {
		return err
	}

	le.Run(ctx)
	return nil
}
