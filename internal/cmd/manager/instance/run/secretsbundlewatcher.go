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

package run

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/fsnotify/fsnotify"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/management/postgres"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/specs"
)

// secretsBundleDataSymlink is the symlink Kubernetes atomically re-points to
// the new content directory whenever it syncs a Secret volume. Reacting only
// to this one marker, instead of every individual file event that leads up
// to it, is what keeps one real bundle update from firing a dozen redundant
// reconciliations.
const secretsBundleDataSymlink = "..data"

// watchSecretsBundleDirectory watches the mounted secrets bundle directory for
// changes and, on every change, sends a GenericEvent for the instance's own
// Cluster into events. The operator updates the bundle's contents whenever a
// Secret/ConfigMap it references changes; since the instance manager has no
// RBAC to watch those directly anymore, this is what triggers a Reconcile()
// so the change actually gets picked up.
func watchSecretsBundleDirectory(
	ctx context.Context,
	instance *postgres.Instance,
	events chan<- event.GenericEvent,
) error {
	contextLogger := log.FromContext(ctx)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("while creating the secrets bundle watcher: %w", err)
	}
	defer func() {
		_ = watcher.Close()
	}()

	if err := watcher.Add(specs.SecretsBundleDirectory); err != nil {
		return fmt.Errorf("while watching the secrets bundle directory: %w", err)
	}

	contextLogger.Info("Started watching the secrets bundle directory", "path", specs.SecretsBundleDirectory)
	defer contextLogger.Info("Stopped watching the secrets bundle directory", "path", specs.SecretsBundleDirectory)

	clusterObject := &apiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.GetClusterName(),
			Namespace: instance.GetNamespaceName(),
		},
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if !ev.Has(fsnotify.Create) || filepath.Base(ev.Name) != secretsBundleDataSymlink {
				// Intermediate step of the atomic update (new content directory
				// being populated, individual files being chmod'd, old directory
				// being removed): the bundle isn't in a consistent state yet.
				continue
			}
			contextLogger.Info("Detected a change in the secrets bundle directory, triggering a reconciliation")
			select {
			case events <- event.GenericEvent{Object: clusterObject}:
			case <-ctx.Done():
				return nil
			}

		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			contextLogger.Error(watchErr, "error while watching the secrets bundle directory")
		}
	}
}
