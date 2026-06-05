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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	schemeBuilder "github.com/cloudnative-pg/cloudnative-pg/internal/scheme"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ensureBootstrapAnnotations", func() {
	ctx := context.Background()
	key := types.NamespacedName{Name: "test-cluster-2", Namespace: "default"}

	build := func(annotations map[string]string) *corev1.PersistentVolumeClaim {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:        key.Name,
				Namespace:   key.Namespace,
				Annotations: annotations,
			},
		}
	}

	It("stamps method and source on an unannotated PVC", func() {
		cli := fake.NewClientBuilder().
			WithScheme(schemeBuilder.BuildWithAllKnownScheme()).
			WithObjects(build(nil)).
			Build()

		Expect(ensureBootstrapAnnotations(
			ctx, cli, key,
			utils.PVCBootstrapMethodPgBasebackup, "test-cluster-1",
		)).To(Succeed())

		var pvc corev1.PersistentVolumeClaim
		Expect(cli.Get(ctx, key, &pvc)).To(Succeed())
		Expect(pvc.Annotations[utils.BootstrapMethodAnnotationName]).
			To(Equal(string(utils.PVCBootstrapMethodPgBasebackup)))
		Expect(pvc.Annotations[utils.BootstrappedFromAnnotationName]).To(Equal("test-cluster-1"))
	})

	It("omits the bootstrapped-from annotation when source is empty", func() {
		cli := fake.NewClientBuilder().
			WithScheme(schemeBuilder.BuildWithAllKnownScheme()).
			WithObjects(build(nil)).
			Build()

		Expect(ensureBootstrapAnnotations(
			ctx, cli, key,
			utils.PVCBootstrapMethodPgBasebackup, "",
		)).To(Succeed())

		var pvc corev1.PersistentVolumeClaim
		Expect(cli.Get(ctx, key, &pvc)).To(Succeed())
		Expect(pvc.Annotations).To(HaveKey(utils.BootstrapMethodAnnotationName))
		Expect(pvc.Annotations).NotTo(HaveKey(utils.BootstrappedFromAnnotationName))
	})

	It("preserves an existing method annotation as a historical record", func() {
		cli := fake.NewClientBuilder().
			WithScheme(schemeBuilder.BuildWithAllKnownScheme()).
			WithObjects(build(map[string]string{
				utils.BootstrapMethodAnnotationName:  string(utils.PVCBootstrapMethodVolumeSnapshot),
				utils.BootstrappedFromAnnotationName: "old-snapshot",
			})).
			Build()

		Expect(ensureBootstrapAnnotations(
			ctx, cli, key,
			utils.PVCBootstrapMethodPgBasebackup, "test-cluster-1",
		)).To(Succeed())

		var pvc corev1.PersistentVolumeClaim
		Expect(cli.Get(ctx, key, &pvc)).To(Succeed())
		Expect(pvc.Annotations[utils.BootstrapMethodAnnotationName]).
			To(Equal(string(utils.PVCBootstrapMethodVolumeSnapshot)))
		Expect(pvc.Annotations[utils.BootstrappedFromAnnotationName]).To(Equal("old-snapshot"))
	})

	It("is a no-op when the PVC does not exist", func() {
		cli := fake.NewClientBuilder().
			WithScheme(schemeBuilder.BuildWithAllKnownScheme()).
			Build()

		Expect(ensureBootstrapAnnotations(
			ctx, cli, key,
			utils.PVCBootstrapMethodPgBasebackup, "test-cluster-1",
		)).To(Succeed())
	})

	It("is a no-op when method is empty", func() {
		cli := fake.NewClientBuilder().
			WithScheme(schemeBuilder.BuildWithAllKnownScheme()).
			WithObjects(build(nil)).
			Build()

		Expect(ensureBootstrapAnnotations(ctx, cli, key, "", "irrelevant")).To(Succeed())

		var pvc corev1.PersistentVolumeClaim
		Expect(cli.Get(ctx, key, &pvc)).To(Succeed())
		Expect(pvc.Annotations).NotTo(HaveKey(utils.BootstrapMethodAnnotationName))
	})
})
