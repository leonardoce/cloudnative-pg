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

package persistentvolumeclaim

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
)

// ensureBootstrapAnnotations records on the targeted PVC how its data
// directory was seeded. It is intentionally write-once: once the
// bootstrap-method annotation exists, the function is a no-op regardless of
// the arguments passed in. The PVC outlives the Pod, so the stamped value
// is a durable historical record — overwriting it on a later reconcile
// would lie about provenance.
//
// `source` may be empty when the data origin has no stable identifier (e.g.
// pg_basebackup against whoever happens to be primary). Returns nil if the
// target PVC is not (yet) present, on the assumption that a later reconcile
// pass will find it.
func ensureBootstrapAnnotations(
	ctx context.Context,
	c client.Client,
	pvcKey types.NamespacedName,
	method utils.PVCBootstrapMethod,
	source string,
) error {
	if method == "" {
		return nil
	}

	var pvc corev1.PersistentVolumeClaim
	if err := c.Get(ctx, pvcKey, &pvc); err != nil {
		if apierrs.IsNotFound(err) {
			return nil
		}
		return err
	}

	if pvc.Annotations[utils.BootstrapMethodAnnotationName] != "" {
		return nil
	}

	patch := client.MergeFrom(pvc.DeepCopy())
	if pvc.Annotations == nil {
		pvc.Annotations = map[string]string{}
	}
	pvc.Annotations[utils.BootstrapMethodAnnotationName] = string(method)
	if source != "" {
		pvc.Annotations[utils.BootstrappedFromAnnotationName] = source
	}
	return c.Patch(ctx, &pvc, patch)
}

// EnsureInstanceBootstrapAnnotations stamps the bootstrap-method (and
// optional source) annotations on every PVC belonging to an instance: PGDATA
// plus WAL and tablespace volumes when present. Stamping the whole group
// keeps the record discoverable through whichever volume an operator
// happens to inspect, since they were all seeded by the same bootstrap.
func EnsureInstanceBootstrapAnnotations(
	ctx context.Context,
	c client.Client,
	cluster *apiv1.Cluster,
	instanceName string,
	method utils.PVCBootstrapMethod,
	source string,
) error {
	var errs []error
	for _, name := range getExpectedInstancePVCNamesFromCluster(cluster, instanceName) {
		key := types.NamespacedName{Namespace: cluster.Namespace, Name: name}
		if err := ensureBootstrapAnnotations(ctx, c, key, method, source); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
