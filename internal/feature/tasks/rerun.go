package featuretasks

import (
	"maps"
	"slices"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// rerunFrom is a new run of src over the nodes of only that src ran, in
// src's order and waves: the same document (src.Rev, not the task as it is
// now), the same inputs and confirmed steps. RerunRun then orders the nodes
// anew over the cluster as it is now; the waves here are src's. Each node starts at its first
// step that did not succeed in src — a failed, unknown, skipped or never
// started one runs again. A node whose every step succeeded starts at 0:
// the operator chose to run it again. The map is the start step by node,
// also kept in the run's StartAt. The caller gives the run its ID, who
// started it and when.
func rerunFrom(src cluster.RunRecord, nodes map[string]cluster.RunNodeRecord, only []string) (cluster.RunRecord, map[string]int) {
	chosen := map[string]bool{}
	for _, id := range only {
		chosen[id] = true
	}
	startAt := map[string]int{}
	var order []string
	var waves [][]string
	for _, wave := range src.Waves {
		var w []string
		for _, id := range wave {
			if !chosen[id] {
				continue
			}
			w = append(w, id)
			order = append(order, id)
			startAt[id] = firstUnsuccessful(nodes[id])
		}
		if len(w) > 0 {
			waves = append(waves, w)
		}
	}
	return cluster.RunRecord{
		TaskID:        src.TaskID,
		TaskName:      src.TaskName,
		Rev:           src.Rev,
		Inputs:        maps.Clone(src.Inputs),
		Nodes:         order,
		Waves:         waves,
		Confirmed:     slices.Clone(src.Confirmed),
		Batch:         src.Batch,
		StopOnFailure: src.StopOnFailure,
		Status:        cluster.RunRunning,
		RerunOf:       src.ID,
		StartAt:       maps.Clone(startAt),
	}, startAt
}

// firstUnsuccessful is the first step of rec that did not succeed, or 0 when
// every recorded step did.
func firstUnsuccessful(rec cluster.RunNodeRecord) int {
	for i, s := range rec.Steps {
		if s.Status != cluster.RunStepSucceeded {
			return i
		}
	}
	return 0
}
