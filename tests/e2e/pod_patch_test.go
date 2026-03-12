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

package e2e

import (
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
	"github.com/cloudnative-pg/cloudnative-pg/tests"
	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/clusterutils"
	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/operator"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pod patch", Label(tests.LabelSmoke, tests.LabelBasic), Serial, Ordered, func() {
	const (
		sampleFile  = fixturesDir + "/base/cluster-storage-class.yaml.template"
		clusterName = "postgresql-storage-class"
		configName  = "cnpg-controller-manager-config"
		level       = tests.Lowest
	)

	var namespace, operatorNamespace string
	var initialConfigMapData map[string]string

	BeforeEach(func() {
		if testLevelEnv.Depth < int(level) {
			Skip("Test depth is lower than the amount requested for this test")
		}
	})

	BeforeAll(func() {
		// Get the operator namespace
		operatorDeployment, err := operator.GetDeployment(env.Ctx, env.Client)
		Expect(err).ToNot(HaveOccurred())
		operatorNamespace = operatorDeployment.GetNamespace()

		// Save the initial ConfigMap data
		configMap := &corev1.ConfigMap{}
		err = env.Client.Get(env.Ctx, ctrlclient.ObjectKey{
			Namespace: operatorNamespace,
			Name:      configName,
		}, configMap)
		if apierrors.IsNotFound(err) {
			initialConfigMapData = nil
		} else {
			Expect(err).ToNot(HaveOccurred())
			initialConfigMapData = configMap.Data
		}

		// Enable the pod patch annotation feature
		By("enabling ENABLE_POD_PATCH_ANNOTATION in operator configuration")
		if configMap.Data == nil {
			configMap.Data = make(map[string]string)
		}
		configMap.Data["ENABLE_POD_PATCH_ANNOTATION"] = "true"

		if apierrors.IsNotFound(err) {
			configMap.Name = configName
			configMap.Namespace = operatorNamespace
			err = env.Client.Create(env.Ctx, configMap)
		} else {
			err = env.Client.Update(env.Ctx, configMap)
		}
		Expect(err).ToNot(HaveOccurred())

		// Reload the operator to pick up the new configuration
		err = operator.ReloadDeployment(env.Ctx, env.Client, 120)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterAll(func() {
		// Restore the initial ConfigMap
		By("restoring initial operator configuration")
		configMap := &corev1.ConfigMap{}
		err := env.Client.Get(env.Ctx, ctrlclient.ObjectKey{
			Namespace: operatorNamespace,
			Name:      configName,
		}, configMap)
		Expect(err).ToNot(HaveOccurred())

		if initialConfigMapData == nil {
			// ConfigMap didn't exist before, delete it
			err = env.Client.Delete(env.Ctx, configMap)
			Expect(err).ToNot(HaveOccurred())
		} else {
			// Restore the original data
			configMap.Data = initialConfigMapData
			err = env.Client.Update(env.Ctx, configMap)
			Expect(err).ToNot(HaveOccurred())
		}

		// Reload the operator to restore original configuration
		err = operator.ReloadDeployment(env.Ctx, env.Client, 120)
		Expect(err).ToNot(HaveOccurred())
	})

	It("use the podPatch annotation to generate Pods", func(_ SpecContext) {
		const namespacePrefix = "cluster-patch-e2e"
		var err error

		namespace, err = env.CreateUniqueTestNamespace(env.Ctx, env.Client, namespacePrefix)
		Expect(err).ToNot(HaveOccurred())

		AssertCreateCluster(namespace, clusterName, sampleFile, env)

		By("adding the podPatch annotation", func() {
			cluster, err := clusterutils.Get(env.Ctx, env.Client, namespace, clusterName)
			Expect(err).ToNot(HaveOccurred())

			patchedCluster := cluster.DeepCopy()

			patchedCluster.SetAnnotations(map[string]string{
				utils.PodPatchAnnotationName: `
					[
						{
							"op": "add",
							"path": "/metadata/annotations/e2e.cnpg.io",
							"value": "this-test"
						}
					]
				`,
			})
			err = env.Client.Patch(env.Ctx, patchedCluster, ctrlclient.MergeFrom(cluster))
			Expect(err).ToNot(HaveOccurred())
		})

		By("deleting all the Pods", func() {
			podList, err := clusterutils.ListPods(env.Ctx, env.Client, namespace, clusterName)
			Expect(err).ToNot(HaveOccurred())

			for i := range podList.Items {
				err := env.Client.Delete(env.Ctx, &podList.Items[i])
				Expect(err).ToNot(HaveOccurred())
			}
		})

		By("waiting for the new annotation to be applied to the new Pods", func() {
			cluster, err := clusterutils.Get(env.Ctx, env.Client, namespace, clusterName)
			Expect(err).ToNot(HaveOccurred())

			timeout := 120
			Eventually(func(g Gomega) {
				podList, err := clusterutils.ListPods(env.Ctx, env.Client, namespace, clusterName)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(podList.Items).To(HaveLen(cluster.Spec.Instances))

				for _, pod := range podList.Items {
					g.Expect(pod.Annotations).To(HaveKeyWithValue("e2e.cnpg.io", "this-test"))
				}
			}, timeout).Should(Succeed())
		})
	})
})
