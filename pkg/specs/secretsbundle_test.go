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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Secrets bundle", func() {
	cluster := &apiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "thisCluster",
			Namespace: "default",
		},
	}

	It("names the bundle after the cluster, with a -bundle suffix", func() {
		Expect(GetSecretsBundleName(cluster.Name)).To(Equal("thisCluster-bundle"))
	})

	It("builds an empty bundle when there are no secrets or configmaps", func() {
		bundle, err := BuildSecretsBundle(cluster, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(bundle.Name).To(Equal("thisCluster-bundle"))
		Expect(bundle.Namespace).To(Equal("default"))
		Expect(bundle.Data).To(BeEmpty())
	})

	It("stores one JSON entry per secret, keyed by secret__<name>, "+
		"decoding straight back into a corev1.Secret with its resourceVersion", func() {
		secrets := map[string]corev1.Secret{
			"superuser": {
				ObjectMeta: metav1.ObjectMeta{
					Name:            "superuser",
					ResourceVersion: "123",
				},
				Data: map[string][]byte{
					"username": []byte("postgres"),
					"password": []byte("secretpassword"),
				},
			},
		}

		bundle, err := BuildSecretsBundle(cluster, secrets, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(bundle.Data).To(HaveLen(1))
		Expect(bundle.Data).To(HaveKey("secret__superuser"))

		var decoded corev1.Secret
		Expect(json.Unmarshal(bundle.Data["secret__superuser"], &decoded)).To(Succeed())
		Expect(decoded.ResourceVersion).To(Equal("123"))
		Expect(decoded.Data).To(Equal(map[string][]byte{
			"username": []byte("postgres"),
			"password": []byte("secretpassword"),
		}))
	})

	It("stores one JSON entry per configmap, keyed by configmap__<name>, "+
		"decoding straight back into a corev1.ConfigMap with Data, BinaryData and resourceVersion intact", func() {
		configMaps := map[string]corev1.ConfigMap{
			"monitoring-queries": {
				ObjectMeta: metav1.ObjectMeta{
					Name:            "monitoring-queries",
					ResourceVersion: "456",
				},
				Data: map[string]string{
					"queries.yaml": "pg_stat: {}",
				},
				BinaryData: map[string][]byte{
					"extra.bin": {0x01, 0x02},
				},
			},
		}

		bundle, err := BuildSecretsBundle(cluster, nil, configMaps)
		Expect(err).ToNot(HaveOccurred())
		Expect(bundle.Data).To(HaveLen(1))
		Expect(bundle.Data).To(HaveKey("configmap__monitoring-queries"))

		var decoded corev1.ConfigMap
		Expect(json.Unmarshal(bundle.Data["configmap__monitoring-queries"], &decoded)).To(Succeed())
		Expect(decoded.ResourceVersion).To(Equal("456"))
		Expect(decoded.Data).To(Equal(map[string]string{
			"queries.yaml": "pg_stat: {}",
		}))
		Expect(decoded.BinaryData).To(Equal(map[string][]byte{
			"extra.bin": {0x01, 0x02},
		}))
	})

	It("bundles secrets and configmaps together without key collisions", func() {
		secrets := map[string]corev1.Secret{
			"shared-name": {ObjectMeta: metav1.ObjectMeta{Name: "shared-name"}},
		}
		configMaps := map[string]corev1.ConfigMap{
			"shared-name": {ObjectMeta: metav1.ObjectMeta{Name: "shared-name"}},
		}

		bundle, err := BuildSecretsBundle(cluster, secrets, configMaps)
		Expect(err).ToNot(HaveOccurred())
		Expect(bundle.Data).To(HaveLen(2))
		Expect(bundle.Data).To(HaveKey("secret__shared-name"))
		Expect(bundle.Data).To(HaveKey("configmap__shared-name"))
	})
})
