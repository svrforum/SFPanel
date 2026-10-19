package system

import (
	"archive/tar"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/config"
)

// agentLiveConfig is an agent's config.yaml as the join writes it.
const agentLiveConfig = `server:
  host: 0.0.0.0
  port: 3628
  stacks_path: /opt/stacks
  tls:
    enabled: true
    dir: /etc/sfpanel/tls
database:
  path: /var/lib/sfpanel/sfpanel.db
auth:
  jwt_secret: agent0agent0agent0agent0agent0agent0agent0agent0agent0agent0abcd
  token_expiry: 24h
docker:
  socket: unix:///var/run/docker.sock
log:
  level: info
cluster:
  enabled: true
  name: lab
  node_id: agent-1
  node_name: worker
  grpc_port: 3629
  data_dir: /var/lib/sfpanel/cluster
  cert_dir: /etc/sfpanel/cluster
  advertise_address: 192.0.2.10
  role: agent
  managers:
    - id: m1
      grpc_address: 192.0.2.1:3629
`

// managerArchiveConfig is a manager's config.yaml as a backup carries it:
// the cluster's JWT key, a manager's cluster block, and settings of its own.
const managerArchiveConfig = `server:
  port: 4000
database:
  path: /var/lib/sfpanel/sfpanel.db
auth:
  jwt_secret: cluster0cluster0cluster0cluster0cluster0cluster0cluster0cluster0
log:
  level: debug
cluster:
  enabled: true
  name: lab
  node_id: m1
  node_name: manager
  grpc_port: 3629
  advertise_address: 192.0.2.1
  raft_tls: true
`

// loadConfig is what the panel boots with from the YAML in data.
func loadConfig(t *testing.T, data []byte) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("the agent would not boot from it: %v\n%s", err, data)
	}
	return cfg
}

// keepLiveClusterBlock is the archive's settings, with the agent's cluster
// block and JWT key whatever the archive carries, in a file the agent boots
// from.
func TestKeepLiveClusterBlock(t *testing.T) {
	live := loadConfig(t, []byte(agentLiveConfig))
	cases := map[string]struct {
		staged string
		check  func(t *testing.T, got *config.Config)
	}{
		"a manager's archive": {
			staged: managerArchiveConfig,
			check: func(t *testing.T, got *config.Config) {
				if got.Server.Port != 4000 || got.Log.Level != "debug" {
					t.Errorf("port %d, log level %q: want the archive's 4000 and debug", got.Server.Port, got.Log.Level)
				}
			},
		},
		"a standalone panel's archive": {
			staged: "server:\n  port: 3628\nauth:\n  jwt_secret: standalone0standalone0standalone0standalone0\n",
		},
		// Every field the archive leaves out keeps the agent's value: an empty
		// one would be database.path: "" (Load refuses it) or docker.socket:
		// "" (no Docker). validateStagedConfig accepts this file as it is.
		"an archive that leaves most settings out": {
			staged: "server:\n  port: 3628\n",
			check: func(t *testing.T, got *config.Config) {
				if got.Database.Path != live.Database.Path || got.Docker.Socket != live.Docker.Socket || !got.Server.TLS.Enabled {
					t.Errorf("database %q, docker %q, tls %v: want the agent's own", got.Database.Path, got.Docker.Socket, got.Server.TLS.Enabled)
				}
			},
		},
		// YAML can set a key without writing it under its own name. None of it
		// reaches the agent's cluster block or key.
		"a merge key at the top": {
			staged: "base: &b\n  cluster:\n    enabled: false\n  auth:\n    jwt_secret: merged0merged0merged0merged0merged0merged0\n<<: *b\nserver:\n  port: 3628\n",
		},
		"an aliased auth block": {
			staged: "a: &a\n  jwt_secret: aliased0aliased0aliased0aliased0aliased0\nauth: *a\nserver:\n  port: 3628\n",
		},
		"a merge key inside auth": {
			staged: "a: &a\n  jwt_secret: aliased0aliased0aliased0aliased0aliased0\nauth:\n  <<: *a\n  token_expiry: 1h\nserver:\n  port: 3628\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			merged, err := keepLiveClusterBlock([]byte(tc.staged), []byte(agentLiveConfig))
			if err != nil {
				t.Fatal(err)
			}
			got := loadConfig(t, merged)
			if !reflect.DeepEqual(got.Cluster, live.Cluster) {
				t.Errorf("cluster block\n got %+v\nwant %+v", got.Cluster, live.Cluster)
			}
			if got.Cluster.Role != config.ClusterRoleAgent || got.Cluster.NodeID != "agent-1" || len(got.Cluster.Managers) != 1 || got.Cluster.Managers[0].ID != "m1" {
				t.Errorf("role %q, node_id %q, managers %+v: want the agent's", got.Cluster.Role, got.Cluster.NodeID, got.Cluster.Managers)
			}
			if got.Auth.JWTSecret != live.Auth.JWTSecret {
				t.Errorf("jwt_secret %q, want the agent's own", got.Auth.JWTSecret)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}

	for name, staged := range map[string]string{
		"not YAML":    "server: [this is not\n  valid: yaml",
		"a list":      "- server\n- port\n",
		"a duplicate": "cluster:\n  role: agent\ncluster:\n  role: manager\n",
	} {
		if _, err := keepLiveClusterBlock([]byte(staged), []byte(agentLiveConfig)); err == nil || !strings.Contains(err.Error(), "archive") {
			t.Errorf("%s: err %v, want a refusal naming the archive", name, err)
		}
	}
}

// The restore itself: an agent restoring a manager's backup is still the same
// agent afterwards, and still holds no cluster JWT key.
func TestAgentRestoreKeepsItsClusterBlock(t *testing.T) {
	withRestartFlushDelay(t, time.Millisecond)
	h, _ := restoreHandler(t)
	h.AgentMode = true
	if err := os.WriteFile(h.ConfigPath, []byte(agentLiveConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := buildTarGz(t, []tarEntry{
		{name: "sfpanel.db", typeflag: tar.TypeReg, body: validSQLiteDBBytes(t)},
		{name: "config.yaml", typeflag: tar.TypeReg, body: []byte(managerArchiveConfig)},
	})
	if rec := runRestore(t, h, archive); rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	data, err := os.ReadFile(h.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	got, live := loadConfig(t, data), loadConfig(t, []byte(agentLiveConfig))
	if !reflect.DeepEqual(got.Cluster, live.Cluster) || got.Auth.JWTSecret != live.Auth.JWTSecret {
		t.Fatalf("restored config is not this agent's:\n%s", data)
	}
	if got.Server.Port != 4000 {
		t.Errorf("port %d, want the archive's 4000", got.Server.Port)
	}
}
