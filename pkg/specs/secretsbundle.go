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

package specs

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

// secretsBundleSuffix is appended to the Cluster name to compute the name
// of the Secret that bundles every Secret/ConfigMap the instance manager
// needs, so it can read them from a mounted volume instead of being granted
// direct API access to them.
const secretsBundleSuffix = "-bundle"

// SecretsBundlePrefix and ConfigMapsBundlePrefix are prepended to the
// original object name to compute the key it is stored under in the
// bundle Secret's Data map. They are exported so the instance manager can
// parse the same encoding back out of the mounted bundle.
const (
	SecretsBundlePrefix    = "secret__"
	ConfigMapsBundlePrefix = "configmap__"
)

// SecretsBundleDirectory is the directory where the secrets bundle is mounted
const SecretsBundleDirectory = "/controller-secrets"

// GetSecretsBundleName returns the name of the Secret that bundles every
// Secret/ConfigMap referenced by the given Cluster
func GetSecretsBundleName(clusterName string) string {
	return clusterName + secretsBundleSuffix
}

// BuildSecretsBundle builds the Secret that bundles every Secret and
// ConfigMap the instance manager needs, given their already-fetched content.
// Each bundled object is stored as its own JSON serialization (ResourceVersion
// included), so the instance manager can decode it straight back into a
// corev1.Secret/corev1.ConfigMap with no separate wire format to keep in
// sync. This is a pure function: the caller is responsible for fetching, via
// GetInvolvedSecretNames/GetInvolvedConfigMapNames, the objects passed in
// secrets/configMaps.
func BuildSecretsBundle(
	cluster *apiv1.Cluster,
	secrets map[string]corev1.Secret,
	configMaps map[string]corev1.ConfigMap,
) (*corev1.Secret, error) {
	data := make(map[string][]byte, len(secrets)+len(configMaps))

	for name, secret := range secrets {
		entry, err := json.Marshal(secret)
		if err != nil {
			return nil, fmt.Errorf("while marshalling secret %q for the bundle: %w", name, err)
		}

		data[SecretsBundlePrefix+name] = entry
	}

	for name, configMap := range configMaps {
		entry, err := json.Marshal(configMap)
		if err != nil {
			return nil, fmt.Errorf("while marshalling configmap %q for the bundle: %w", name, err)
		}

		data[ConfigMapsBundlePrefix+name] = entry
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetSecretsBundleName(cluster.Name),
			Namespace: cluster.Namespace,
		},
		Data: data,
	}, nil
}
