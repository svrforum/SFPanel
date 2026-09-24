package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dockerclient "github.com/docker/docker/client"
)

func TestNetworkConnectionsPreserveContainerAndNeverForceDisconnect(t *testing.T) {
	var operations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		var body struct {
			Container string
			Force     bool
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Container != "container-id" || body.Force {
			t.Errorf("unexpected payload: %+v", body)
		}
		operations = append(operations, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(server.URL), dockerclient.WithVersion("1.43"), dockerclient.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	c := &Client{cli: cli}
	if err := c.ConnectNetwork(context.Background(), "network-id", "container-id"); err != nil {
		t.Fatal(err)
	}
	if err := c.DisconnectNetwork(context.Background(), "network-id", "container-id"); err != nil {
		t.Fatal(err)
	}
	if len(operations) != 2 || !strings.HasSuffix(operations[0], "/networks/network-id/connect") || !strings.HasSuffix(operations[1], "/networks/network-id/disconnect") {
		t.Fatalf("wrong operations: %v", operations)
	}
}
