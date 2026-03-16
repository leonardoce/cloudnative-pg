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

package status

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/management/postgres/webserver/client/remote"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/management/url"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/postgres"
)

// extractInstancesStatus extracts the instance status from the given pod list
func extractInstancesStatus(
	ctx context.Context,
	cluster *apiv1.Cluster,
	config *rest.Config,
	filteredPods []corev1.Pod,
	timeoutPerInstance time.Duration,
) (postgres.PostgresqlStatusList, []error) {
	result := postgres.PostgresqlStatusList{
		IsReplicaCluster: cluster.IsReplica(),
		CurrentPrimary:   cluster.Status.CurrentPrimary,
	}
	var errs []error

	for idx := range filteredPods {
		instanceStatus := getInstanceStatusFromPod(
			ctx, config, filteredPods[idx], timeoutPerInstance)
		result.Items = append(result.Items, instanceStatus)
		if instanceStatus.Error != nil {
			errs = append(errs, instanceStatus.Error)
		}
	}

	return result, errs
}

func getInstanceStatusFromPod(
	ctx context.Context,
	config *rest.Config,
	pod corev1.Pod,
	timeout time.Duration,
) postgres.PostgresqlStatus {
	var result postgres.PostgresqlStatus

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer func() {
		cancel()
	}()
	statusResult, err := kubernetes.NewForConfigOrDie(config).
		CoreV1().
		Pods(pod.Namespace).
		ProxyGet(
			remote.GetStatusSchemeFromPod(&pod).ToString(),
			pod.Name,
			strconv.Itoa(int(url.StatusPort)),
			url.PathPgStatus,
			nil,
		).
		DoRaw(timeoutCtx)
	if err != nil {
		result.AddPod(pod)
		result.Error = fmt.Errorf(
			"failed to get status by proxying to the pod, you might lack permissions to get pods/proxy: %w",
			err)
		return result
	}

	if err := json.Unmarshal(statusResult, &result); err != nil {
		result.Error = fmt.Errorf("can't parse pod output")
	}

	result.AddPod(pod)

	return result
}
