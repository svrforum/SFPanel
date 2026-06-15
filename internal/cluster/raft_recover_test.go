package cluster

import (
	"testing"

	"github.com/hashicorp/raft"
)

func srv(id, addr string, nonVoter bool) raft.Server {
	suffrage := raft.Voter
	if nonVoter {
		suffrage = raft.Nonvoter
	}
	return raft.Server{Suffrage: suffrage, ID: raft.ServerID(id), Address: raft.ServerAddress(addr)}
}

func TestValidateRecoveryServers(t *testing.T) {
	tests := []struct {
		name    string
		servers []raft.Server
		localID string
		wantErr bool
	}{
		{
			name:    "valid set including local node",
			servers: []raft.Server{srv("a", "10.0.0.1:3630", false), srv("b", "10.0.0.2:3630", false)},
			localID: "a",
			wantErr: false,
		},
		{
			name:    "single-node recovery with local present",
			servers: []raft.Server{srv("a", "127.0.0.1:3630", false)},
			localID: "a",
			wantErr: false,
		},
		{
			name:    "empty server list is rejected",
			servers: nil,
			localID: "a",
			wantErr: true,
		},
		{
			name:    "local node absent is rejected",
			servers: []raft.Server{srv("b", "10.0.0.2:3630", false), srv("c", "10.0.0.3:3630", false)},
			localID: "a",
			wantErr: true,
		},
		{
			name:    "address without port is rejected",
			servers: []raft.Server{srv("a", "10.0.0.1", false)},
			localID: "a",
			wantErr: true,
		},
		{
			name:    "empty address is rejected",
			servers: []raft.Server{srv("a", "", false)},
			localID: "a",
			wantErr: true,
		},
		{
			name:    "local node present as non-voter still passes",
			servers: []raft.Server{srv("a", "10.0.0.1:3630", true), srv("b", "10.0.0.2:3630", false)},
			localID: "a",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRecoveryServers(tt.servers, tt.localID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRecoveryServers() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
