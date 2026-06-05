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
	"errors"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

// ErrNoQualifyingBackup indicates that `spec.replicaBootstrap.recovery` is
// set on the cluster but no successful Backup matches the requested method
// (and plugin name, when applicable). The caller is expected to surface
// this as a cold-start refusal rather than silently fall back to
// pg_basebackup.
var ErrNoQualifyingBackup = errors.New("no successful Backup matches replicaBootstrap.recovery")

// ErrWALArchivingRequired indicates that `spec.replicaBootstrap.recovery`
// is set but the cluster has no WAL archive (neither
// `spec.backup.barmanObjectStore` nor a WAL-archiver plugin). Without an
// archive, a replica restored from a backup cannot catch up on WAL beyond
// what the primary still holds, so the recovery path is unsafe.
var ErrWALArchivingRequired = errors.New(
	"replicaBootstrap.recovery requires WAL archiving (barmanObjectStore or a WAL-archiver plugin)")

// selectReplicaBootstrapBackup picks the Backup that should seed a new
// replica when the cluster opts in via `spec.replicaBootstrap.recovery`.
// The function is pure: it filters and sorts the supplied list, never
// touching the API server.
//
// Returns:
//   - (nil, nil)                    when replicaBootstrap.recovery is unset;
//     the caller keeps today's behaviour.
//   - (backup, nil)                 when at least one Backup qualifies; the
//     most recent one is returned.
//   - (nil, ErrWALArchivingRequired) when the cluster has no WAL archive.
//   - (nil, ErrNoQualifyingBackup)  when the user opted in but nothing
//     matches the requested method, plugin, version and age constraints.
func selectReplicaBootstrapBackup(
	cluster *apiv1.Cluster,
	backupList apiv1.BackupList,
) (*apiv1.Backup, error) {
	if cluster.Spec.ReplicaBootstrap == nil || cluster.Spec.ReplicaBootstrap.Recovery == nil {
		return nil, nil
	}

	if !isWALArchivingActive(cluster) {
		return nil, ErrWALArchivingRequired
	}

	recovery := cluster.Spec.ReplicaBootstrap.Recovery
	clusterMajor := knownClusterMajorVersion(cluster)

	backupList.SortByReverseCreationTime()
	for idx := range backupList.Items {
		candidate := &backupList.Items[idx]
		if qualifiesAsReplicaBootstrapBackup(candidate, cluster, recovery, clusterMajor) {
			return candidate, nil
		}
	}

	return nil, ErrNoQualifyingBackup
}

// knownClusterMajorVersion returns the cluster's Postgres major version,
// or 0 when it cannot yet be determined (image info not populated, or
// version string not parseable). 0 instructs the selector to skip the
// version-match check rather than reject every candidate.
func knownClusterMajorVersion(cluster *apiv1.Cluster) int {
	if cluster.Status.PGDataImageInfo == nil {
		return 0
	}
	major, err := cluster.GetPostgresqlMajorVersion()
	if err != nil {
		return 0
	}
	return major
}

// isWALArchivingActive reports whether the cluster has somewhere to fetch
// WAL from beyond what the primary still holds locally. A replica seeded
// from a Backup may need WAL the primary has already rotated away, so an
// archive (barmanObjectStore or a WAL-archiver plugin) is mandatory.
func isWALArchivingActive(cluster *apiv1.Cluster) bool {
	if cluster.Spec.Backup != nil && cluster.Spec.Backup.BarmanObjectStore != nil {
		return true
	}
	return cluster.GetEnabledWALArchivePluginName() != ""
}

// qualifiesAsReplicaBootstrapBackup applies the per-Backup filters used by
// selectReplicaBootstrapBackup. `clusterMajor` is 0 when the cluster's
// major version cannot yet be determined (see knownClusterMajorVersion);
// in that case the version-match check is skipped.
func qualifiesAsReplicaBootstrapBackup(
	backup *apiv1.Backup,
	cluster *apiv1.Cluster,
	recovery *apiv1.ReplicaBootstrapRecovery,
	clusterMajor int,
) bool {
	if backup.Status.Phase != apiv1.BackupPhaseCompleted {
		return false
	}

	// BackupSpec.Method defaults to barmanObjectStore at the CRD level;
	// treat an empty value identically so older Backups remain selectable.
	method := backup.Spec.Method
	if method == "" {
		method = apiv1.BackupMethodBarmanObjectStore
	}
	if method != recovery.Method {
		return false
	}

	if recovery.Method == apiv1.BackupMethodPlugin {
		if backup.Spec.PluginConfiguration == nil ||
			backup.Spec.PluginConfiguration.Name != recovery.PluginName {
			return false
		}
	}

	// A Backup from a previous incarnation of a same-named Cluster would
	// restore data that does not belong to this cluster's timeline.
	if backup.CreationTimestamp.Before(&cluster.CreationTimestamp) {
		return false
	}

	// Major-version match. Only enforced once we actually know the
	// cluster's version (i.e. PGDataImageInfo has been populated by a
	// previous reconcile) and the backup has recorded its own.
	if clusterMajor != 0 {
		backupMajor := backup.Status.MajorVersion
		if backupMajor == 0 || backupMajor != clusterMajor {
			return false
		}
	}

	return true
}
