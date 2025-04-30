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
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/pkg/certs"
	cnpgUrl "github.com/cloudnative-pg/cloudnative-pg/pkg/management/url"
	postgresSpec "github.com/cloudnative-pg/cloudnative-pg/pkg/postgres"
)

// instanceReachabilityCheckerConfiguration if the configuration of the instance
// reachability checker
type instanceReachabilityCheckerConfiguration struct {
	requestTimeout    time.Duration
	connectionTimeout time.Duration
}

// instanceReachabilityChecker can check if a certain instance is reachable by using
// the failsafe REST endpoint
type instanceReachabilityChecker struct {
	dialer *net.Dialer
	client *http.Client
}

// instanceCoordinates contains the information needed to reach the REST server of a certain instance
type instanceCoordinates struct {
	name string
	ip   string
}

// newInstanceReachabilityChecker creates a new instance reachability checker by loading
// the server CA certificate from the same location that will be used by PostgreSQL.
// In this case, we avoid using the API Server as it may be unreliable.
func newInstanceReachabilityChecker(
	cfg instanceReachabilityCheckerConfiguration,
) (*instanceReachabilityChecker, error) {
	certificateLocation := postgresSpec.ServerCACertificateLocation
	caCertificate, err := os.ReadFile(certificateLocation) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("while reading server CA certificate [%s]: %w", certificateLocation, err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCertificate)

	tlsConfig := certs.NewTLSConfigFromCertPool(caCertPool)

	dialer := &net.Dialer{Timeout: cfg.connectionTimeout}

	client := http.Client{
		Transport: &http.Transport{
			DialContext:     dialer.DialContext,
			TLSClientConfig: tlsConfig,
		},
		Timeout: cfg.requestTimeout,
	}

	return &instanceReachabilityChecker{
		dialer: dialer,
		client: &client,
	}, nil
}

// ensureInstanceIsReachable checks if the instance with the passed coordinates is reachable
// by calling the failsafe endpoint.
func (e *instanceReachabilityChecker) ensureInstanceIsReachable(key instanceCoordinates) error {
	failsafeURL := url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s:%d", key.ip, cnpgUrl.StatusPort),
		Path:   cnpgUrl.PathFailSafe,
	}

	var res *http.Response
	var err error
	if res, err = e.client.Get(failsafeURL.String()); err != nil {
		return &instanceConnectivityError{
			key: key,
			err: err,
		}
	}

	_ = res.Body.Close()

	return nil
}

// instanceConnectivityError is raised when the instance connectivity test failed.
type instanceConnectivityError struct {
	key instanceCoordinates
	err error
}

// Error implements the error interface
func (e *instanceConnectivityError) Error() string {
	return fmt.Sprintf(
		"instance connectivity error for instance [%s] with ip [%s]: %s",
		e.key.name,
		e.key.ip,
		e.err.Error())
}

// Unwrap implements the error interface
func (e *instanceConnectivityError) Unwrap() error {
	return e.err
}
