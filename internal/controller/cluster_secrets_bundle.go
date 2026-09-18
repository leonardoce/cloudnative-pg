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

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/specs"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
)

// fetchInvolvedSecrets fetches every Secret enumerated by
// specs.GetInvolvedSecretNames, keyed by name. A referenced Secret that
// doesn't exist yet (e.g. still being created by another controller) is
// silently omitted, matching the tolerance the instance manager's own
// on-demand Get calls have today.
func (r *ClusterReconciler) fetchInvolvedSecrets(
	ctx context.Context,
	cluster *apiv1.Cluster,
	roleOptions specs.RoleOptions,
) (map[string]corev1.Secret, error) {
	names := specs.GetInvolvedSecretNames(roleOptions)
	result := make(map[string]corev1.Secret, len(names))

	for _, name := range names {
		var secret corev1.Secret
		if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &secret); err != nil {
			if apierrs.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("while getting secret %q for the bundle: %w", name, err)
		}
		result[name] = secret
	}

	return result, nil
}

// fetchInvolvedConfigMaps fetches every ConfigMap enumerated by
// specs.GetInvolvedConfigMapNames, keyed by name, with the same
// "skip if not found yet" tolerance as fetchInvolvedSecrets.
func (r *ClusterReconciler) fetchInvolvedConfigMaps(
	ctx context.Context,
	cluster *apiv1.Cluster,
) (map[string]corev1.ConfigMap, error) {
	names := specs.GetInvolvedConfigMapNames(cluster)
	result := make(map[string]corev1.ConfigMap, len(names))

	for _, name := range names {
		var configMap corev1.ConfigMap
		if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &configMap); err != nil {
			if apierrs.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("while getting configmap %q for the bundle: %w", name, err)
		}
		result[name] = configMap
	}

	return result, nil
}

// reconcileBundle ensures that the Secret bundling every Secret/ConfigMap
// the instance manager needs exists and is up to date. This is what lets
// the instance manager read its secrets from a mounted volume instead of
// being granted direct API access to them.
func (r *ClusterReconciler) reconcileBundle(ctx context.Context, cluster *apiv1.Cluster) error {
	roleOptions, err := r.buildRoleOptions(ctx, cluster)
	if err != nil {
		return err
	}

	secrets, err := r.fetchInvolvedSecrets(ctx, cluster, roleOptions)
	if err != nil {
		return err
	}

	configMaps, err := r.fetchInvolvedConfigMaps(ctx, cluster)
	if err != nil {
		return err
	}

	proposedBundle, err := specs.BuildSecretsBundle(cluster, secrets, configMaps)
	if err != nil {
		return fmt.Errorf("while building the secrets bundle: %w", err)
	}
	cluster.SetInheritedDataAndOwnership(&proposedBundle.ObjectMeta)

	var currentBundle corev1.Secret
	if err := r.Get(
		ctx,
		client.ObjectKey{Namespace: proposedBundle.Namespace, Name: proposedBundle.Name},
		&currentBundle,
	); err != nil {
		if apierrs.IsNotFound(err) {
			return r.Create(ctx, proposedBundle)
		}
		return fmt.Errorf("while getting secrets bundle: %w", err)
	}

	if equality.Semantic.DeepEqual(currentBundle.Data, proposedBundle.Data) &&
		equality.Semantic.DeepEqual(currentBundle.Labels, proposedBundle.Labels) &&
		equality.Semantic.DeepEqual(currentBundle.Annotations, proposedBundle.Annotations) {
		return nil
	}

	patchedBundle := currentBundle.DeepCopy()
	patchedBundle.Data = proposedBundle.Data
	utils.MergeObjectsMetadata(patchedBundle, proposedBundle)

	return r.Patch(ctx, patchedBundle, client.MergeFrom(&currentBundle))
}
