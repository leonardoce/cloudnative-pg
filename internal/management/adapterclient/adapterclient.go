/*
Copyright The CloudNativePG Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package adapterclient

import (
	"log"

	"github.com/leonardoce/backup-adapter/pkg/adapter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	socketPath = "unix:///controller/walmanager"
)

// AdapterClient represent a client for the WAL management sidecas
type AdapterClient struct {
	conn                *grpc.ClientConn
	walManagerClient    adapter.WalManagerClient
	backupManagerClient adapter.BackupManagerClient
}

// NewClient creates a new adapter client
func NewClient() (*AdapterClient, error) {
	// Set up a connection to the server.
	conn, err := grpc.Dial(socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}

	walManagerClient := adapter.NewWalManagerClient(conn)
	return &AdapterClient{
		conn:             conn,
		walManagerClient: walManagerClient,
	}, nil
}

// Close closes the underlying connection
func (cli *AdapterClient) Close() error {
	return cli.conn.Close()
}

// WalManagerClient returns the WAL management client interface
func (cli *AdapterClient) WalManagerClient() adapter.WalManagerClient {
	return cli.walManagerClient
}

// BackupManagerClient returns the WAL management client interface
func (cli *AdapterClient) BackupManagerClient() adapter.BackupManagerClient {
	return cli.backupManagerClient
}
