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
	"time"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Liveness probes annotations parser", func() {
	DescribeTable(
		"Annotations decoder",
		func(ctx SpecContext, annotations map[string]string, name string, defaultValue time.Duration, expected time.Duration) {
			result := getConfigFromAnnotation(ctx, annotations, name, defaultValue)
			Expect(result).To(Equal(expected))
		},
		Entry(
			"correct value",
			map[string]string{
				"test": "1234",
			},
			"test",
			500*time.Millisecond,
			1234*time.Millisecond,
		),
		Entry(
			"value is not an integer",
			map[string]string{
				"test": "12iii34",
			},
			"test",
			500*time.Millisecond,
			500*time.Millisecond,
		),
		Entry(
			"entry does not exist",
			map[string]string{
				"test": "12iii34",
			},
			"toast",
			500*time.Millisecond,
			500*time.Millisecond,
		),
	)

	DescribeTable(
		"Annotations parser",
		func(ctx SpecContext, cluster *apiv1.Cluster, expected pingerConfig) {
			result := getPingerConfig(ctx, cluster)
			Expect(result).To(Equal(expected))
		},
		Entry(
			"Empty cluster",
			&apiv1.Cluster{},
			pingerConfig{
				requestTimeout:    defaultRequestTimeout,
				connectionTimeout: defaultConnectionTimeout,
			},
		),
		Entry(
			"Cluster with annotations",
			&apiv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						utils.InstancePingerConnectionTimeoutAnnotationName: "1234",
						utils.InstancePingerRequestTimeoutAnnotationName:    "4321",
					},
				},
			},
			pingerConfig{
				connectionTimeout: 1234 * time.Millisecond,
				requestTimeout:    4321 * time.Millisecond,
			},
		),
	)
})
