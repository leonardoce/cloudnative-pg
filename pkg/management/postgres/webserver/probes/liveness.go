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

package probes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/management/postgres"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
)

const (
	defaultConnectionTimeout = 500 * time.Millisecond
	defaultRequestTimeout    = 500 * time.Millisecond
)

type livenessExecutor struct {
	cli      client.Client
	instance *postgres.Instance

	lastestKnownCluster *apiv1.Cluster
}

// NewLivenessChecker creates a new instance of the liveness probe checker
func NewLivenessChecker(
	cli client.Client,
	instance *postgres.Instance,
) Checker {
	return &livenessExecutor{
		cli:      cli,
		instance: instance,
	}
}

func (e *livenessExecutor) IsHealthy(
	ctx context.Context,
	w http.ResponseWriter,
) {
	contextLogger := log.FromContext(ctx)

	isPrimary, err := e.instance.IsPrimary()
	if err != nil {
		contextLogger.Error(
			err,
			"Error while checking the instance role, skipping automatic shutdown.")
		_, _ = fmt.Fprint(w, "OK")
		return
	}

	if !isPrimary {
		// There's no need to restart a replica if isolated
		_, _ = fmt.Fprint(w, "OK")
		return
	}

	var cluster apiv1.Cluster
	err = e.cli.Get(
		ctx,
		client.ObjectKey{Namespace: e.instance.GetNamespaceName(), Name: e.instance.GetClusterName()},
		&cluster,
	)
	if err == nil {
		// We were able to reach the API server. Everything is right.
		_, _ = fmt.Fprint(w, "OK")

		// Even if we reach this point concurrently, assignment is an atomic
		// operation and it would not represent a problem.
		e.lastestKnownCluster = &cluster

		// We correctly reached the API server but, as a failsafe measure, we
		// exercise the rechability checker and leave a log message if something
		// is not right.
		// In this way a network configuration problem can be discovered as
		// quickly as possible.
		e.reachabilityCheckerExercise(ctx)

		return
	}

	if e.lastestKnownCluster == nil {
		// We were never able to download a cluster definition. This should not
		// happen because we check the API server connectivity as soon as the
		// instance manager starts, before starting the probe web server.
		//
		// To be safe, we classify this instance manager to be not isolated and
		// postpone any decision to a later liveness probe call.
		contextLogger.Warning(
			"The API server is not reachable and no cluster definition has been received, " +
				"skipping automatic shutdown.")

		_, _ = fmt.Fprint(w, "OK")
		return
	}

	if e.lastestKnownCluster.Spec.Instances == 1 {
		// There will be just one instance, and it will be not possible to have
		// two primaries at the same time.
		contextLogger.Warning(
			"The API server is not reachable in a single-instance cluster, " +
				"skipping automatic shutdown.")

		_, _ = fmt.Fprint(w, "OK")
		return
	}

	// We are isolated from the API server. We use the failsafe entrypoint to
	// check if we're isolated from the other PG instances too.
	contextLogger.Warning(
		"The API server is not reachable, triggering instance connectivity check")
	if err := e.ensureInstancesAreReachable(ctx); err != nil {
		contextLogger.Error(err, "Instance connectivity error, liveness probe failing")
		http.Error(
			w,
			fmt.Sprintf("liveness check failed: %s", err.Error()),
			http.StatusInternalServerError,
		)
		return
	}

	contextLogger.Info(
		"Instance connectivity test succeeded, liveness probe succeeding",
		"latestKnownInstancesReportedState", e.lastestKnownCluster.Status.InstancesReportedState,
	)

	_, _ = fmt.Fprint(w, "OK")
}

func (e *livenessExecutor) reachabilityCheckerExercise(ctx context.Context) {
	contextLogger := log.FromContext(ctx)

	if err := e.ensureInstancesAreReachable(ctx); err != nil {
		contextLogger.Error(err, "Instance connectivity test failed, skipping")
		return
	}
}

func (e *livenessExecutor) ensureInstancesAreReachable(ctx context.Context) error {
	cluster := e.lastestKnownCluster
	cfg := getPingerConfig(ctx, cluster)

	pinger, err := newPinger(cfg)
	if err != nil {
		return err
	}

	for name, state := range cluster.Status.InstancesReportedState {
		if err := pinger.ping(state.IP); err != nil {
			return fmt.Errorf("for instance %s: %w", name, err)
		}
	}

	return nil
}

func getPingerConfig(ctx context.Context, cluster *apiv1.Cluster) pingerConfig {
	result := pingerConfig{
		connectionTimeout: getConfigFromAnnotation(
			ctx,
			cluster.ObjectMeta.Annotations,
			utils.InstancePingerConnectionTimeoutAnnotationName,
			defaultConnectionTimeout,
		),
		requestTimeout: getConfigFromAnnotation(
			ctx,
			cluster.ObjectMeta.Annotations,
			utils.InstancePingerRequestTimeoutAnnotationName,
			defaultRequestTimeout,
		),
	}

	return result
}

func getConfigFromAnnotation(
	ctx context.Context,
	annotations map[string]string,
	name string,
	defaultValue time.Duration,
) time.Duration {
	contextLogger := log.FromContext(ctx)

	value, ok := annotations[name]
	if !ok {
		return defaultValue
	}

	intValue, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		contextLogger.Error(err, "Wrong annotation value", "value", value, "name", name)
		return defaultValue
	}

	return time.Duration(intValue) * time.Millisecond
}
