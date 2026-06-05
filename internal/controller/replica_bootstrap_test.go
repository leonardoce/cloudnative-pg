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
	"time"

	barmanApi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("selectReplicaBootstrapBackup", func() {
	const pluginName = "demo.cnpg.io"

	clusterCreated := metav1.NewTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))

	// newCluster builds a base cluster opted in to replica bootstrap from
	// the given backup method. Callers tweak the result before passing it
	// to the selector.
	newCluster := func(method apiv1.BackupMethod) *apiv1.Cluster {
		c := &apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "test-cluster",
				Namespace:         "default",
				CreationTimestamp: clusterCreated,
			},
			Spec: apiv1.ClusterSpec{
				Backup: &apiv1.BackupConfiguration{
					BarmanObjectStore: &barmanApi.BarmanObjectStoreConfiguration{
						DestinationPath: "s3://bucket/path",
					},
				},
				ReplicaBootstrap: &apiv1.ReplicaBootstrapConfiguration{
					Recovery: &apiv1.ReplicaBootstrapRecovery{
						Method: method,
					},
				},
			},
		}
		if method == apiv1.BackupMethodPlugin {
			c.Spec.ReplicaBootstrap.Recovery.PluginName = pluginName
		}
		return c
	}

	// newBackup constructs a completed Backup of the given method. Plugin
	// backups get their plugin name set to match the cluster by default.
	newBackup := func(name string, method apiv1.BackupMethod, created time.Time) *apiv1.Backup {
		b := &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(created),
			},
			Spec: apiv1.BackupSpec{
				Method: method,
			},
			Status: apiv1.BackupStatus{
				Phase: apiv1.BackupPhaseCompleted,
			},
		}
		if method == apiv1.BackupMethodPlugin {
			b.Spec.PluginConfiguration = &apiv1.BackupPluginConfiguration{Name: pluginName}
		}
		return b
	}

	It("returns (nil, nil) when replicaBootstrap is unset", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		cluster.Spec.ReplicaBootstrap = nil

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{
				*newBackup("b1", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(time.Hour)),
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(backup).To(BeNil())
	})

	It("returns (nil, nil) when replicaBootstrap.recovery is unset", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		cluster.Spec.ReplicaBootstrap.Recovery = nil
		cluster.Spec.ReplicaBootstrap.PgBasebackup = &apiv1.ReplicaBootstrapPgBaseBackup{}

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{})
		Expect(err).NotTo(HaveOccurred())
		Expect(backup).To(BeNil())
	})

	It("refuses when WAL archiving is not configured", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		cluster.Spec.Backup = nil

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{
				*newBackup("b1", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(time.Hour)),
			},
		})
		Expect(err).To(MatchError(ErrWALArchivingRequired))
		Expect(backup).To(BeNil())
	})

	It("returns ErrNoQualifyingBackup when the list is empty", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{})
		Expect(err).To(MatchError(ErrNoQualifyingBackup))
		Expect(backup).To(BeNil())
	})

	It("picks the latest qualifying backup", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		old := newBackup("old", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(time.Hour))
		newer := newBackup("newer", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(2*time.Hour))

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{*old, *newer},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(backup).NotTo(BeNil())
		Expect(backup.Name).To(Equal("newer"))
	})

	It("skips backups of a non-matching method", func() {
		cluster := newCluster(apiv1.BackupMethodVolumeSnapshot)
		wrong := newBackup("wrong-method", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(2*time.Hour))
		match := newBackup("match", apiv1.BackupMethodVolumeSnapshot, clusterCreated.Add(time.Hour))

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{*wrong, *match},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(backup.Name).To(Equal("match"))
	})

	It("treats an empty BackupSpec.Method as barmanObjectStore", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		legacy := newBackup("legacy", "", clusterCreated.Add(time.Hour))

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{*legacy},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(backup.Name).To(Equal("legacy"))
	})

	It("filters plugin backups by plugin name", func() {
		cluster := newCluster(apiv1.BackupMethodPlugin)
		other := newBackup("other-plugin", apiv1.BackupMethodPlugin, clusterCreated.Add(2*time.Hour))
		other.Spec.PluginConfiguration.Name = "other.cnpg.io"
		match := newBackup("match", apiv1.BackupMethodPlugin, clusterCreated.Add(time.Hour))

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{*other, *match},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(backup.Name).To(Equal("match"))
	})

	It("skips non-completed backups", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		running := newBackup("running", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(2*time.Hour))
		running.Status.Phase = apiv1.BackupPhaseRunning
		good := newBackup("good", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(time.Hour))

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{*running, *good},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(backup.Name).To(Equal("good"))
	})

	It("skips backups taken before the cluster was created", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		stale := newBackup("stale", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(-time.Hour))

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{*stale},
		})
		Expect(err).To(MatchError(ErrNoQualifyingBackup))
		Expect(backup).To(BeNil())
	})

	It("enforces major-version match when the cluster version is known", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		cluster.Spec.ImageCatalogRef = &apiv1.ImageCatalogRef{Major: 17}
		cluster.Status.PGDataImageInfo = &apiv1.ImageInfo{MajorVersion: 17}

		mismatch := newBackup("mismatch", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(2*time.Hour))
		mismatch.Status.MajorVersion = 16
		match := newBackup("match", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(time.Hour))
		match.Status.MajorVersion = 17

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{*mismatch, *match},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(backup.Name).To(Equal("match"))
	})

	It("skips the version check when PGDataImageInfo is unset", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		cluster.Spec.ImageCatalogRef = &apiv1.ImageCatalogRef{Major: 17}
		// Status.PGDataImageInfo left nil to simulate an early reconcile.

		mismatch := newBackup("mismatch", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(time.Hour))
		mismatch.Status.MajorVersion = 16

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{*mismatch},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(backup.Name).To(Equal("mismatch"))
	})

	It("rejects a backup with an unrecorded major version when the cluster version is known", func() {
		cluster := newCluster(apiv1.BackupMethodBarmanObjectStore)
		cluster.Spec.ImageCatalogRef = &apiv1.ImageCatalogRef{Major: 17}
		cluster.Status.PGDataImageInfo = &apiv1.ImageInfo{MajorVersion: 17}

		blank := newBackup("blank", apiv1.BackupMethodBarmanObjectStore, clusterCreated.Add(time.Hour))
		// blank.Status.MajorVersion left as zero

		backup, err := selectReplicaBootstrapBackup(cluster, apiv1.BackupList{
			Items: []apiv1.Backup{*blank},
		})
		Expect(err).To(MatchError(ErrNoQualifyingBackup))
		Expect(backup).To(BeNil())
	})
})
