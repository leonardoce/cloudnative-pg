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

// Package bundleclient provides a client.Client decorator that serves
// Secret/ConfigMap Get calls from the operator-built secrets bundle mounted
// on disk, instead of hitting the Kubernetes API. This is what lets the
// instance manager work without any "secrets"/"configmaps" RBAC.
package bundleclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudnative-pg/cloudnative-pg/pkg/specs"
)

// Client decorates a client.Client, serving Get calls for Secrets and
// ConfigMaps from the mounted secrets bundle, and passing every other
// request through to the wrapped client unchanged.
type Client struct {
	client.Client
}

// NewClient returns a Client reading the secrets bundle, falling back to
// inner for anything that isn't a Secret or a ConfigMap Get.
func NewClient(inner client.Client) *Client {
	return &Client{Client: inner}
}

// Get implements client.Reader
func (c *Client) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	switch typedObj := obj.(type) {
	case *corev1.Secret:
		return readBundleEntry(specs.SecretsBundlePrefix, key.Name, corev1.Resource("secrets"), typedObj)
	case *corev1.ConfigMap:
		return readBundleEntry(specs.ConfigMapsBundlePrefix, key.Name, corev1.Resource("configmaps"), typedObj)
	default:
		return c.Client.Get(ctx, key, obj, opts...)
	}
}

// readBundleEntry reads the bundle file for the given prefix and name and
// JSON-decodes it directly into out (a *corev1.Secret or *corev1.ConfigMap),
// which is exactly what was passed to BuildSecretsBundle when the bundle was
// built, ResourceVersion included. It returns a Kubernetes NotFound error
// (using the given GroupResource) if the object isn't in the bundle.
func readBundleEntry(prefix, name string, resource schema.GroupResource, out any) error {
	path := filepath.Join(specs.SecretsBundleDirectory, prefix+name)

	content, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return apierrors.NewNotFound(resource, name)
		}
		return fmt.Errorf("while reading bundle entry %q: %w", path, err)
	}

	if err := json.Unmarshal(content, out); err != nil {
		return fmt.Errorf("while unmarshalling bundle entry %q: %w", path, err)
	}

	return nil
}
