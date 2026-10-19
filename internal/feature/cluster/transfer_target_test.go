package featurecluster

import (
	"testing"

	"github.com/svrforum/SFPanel/internal/cluster"
)

func TestIsTransferTarget(t *testing.T) {
	cases := []struct {
		voter, manager bool
		status         cluster.NodeStatus
		want           bool
	}{
		{true, true, cluster.StatusOnline, true},
		{true, true, cluster.StatusSuspect, false},
		{false, true, cluster.StatusOnline, false},  // a manager still joining
		{false, false, cluster.StatusOnline, false}, // an agent
	}
	for _, tc := range cases {
		if got := isTransferTarget(tc.voter, tc.manager, tc.status); got != tc.want {
			t.Errorf("isTransferTarget(%v, %v, %s) = %v", tc.voter, tc.manager, tc.status, got)
		}
	}
}
