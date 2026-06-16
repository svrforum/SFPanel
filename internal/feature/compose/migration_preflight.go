package compose

// PreflightInput are the facts the orchestrator gathers (target queried over the
// cluster) and feeds to the pure report builder.
type PreflightInput struct {
	SourceNodeID, TargetNodeID string
	SourceArch, TargetArch     string
	TargetFreeBytes            int64
	EstimatedBytes             int64
	StackPorts                 []int
	TargetPortsInUse           []int
	TargetStackExists          bool
	OverwriteAcked             bool
	HasSystemBind              bool
	HasExternalVolume          bool
	HasDevice                  bool
	Disposition                Disposition
}

type PreflightFinding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type PreflightReport struct {
	Blocks   []PreflightFinding `json:"blocks"`
	Warnings []PreflightFinding `json:"warnings"`
}

func intersect(a, b []int) []int {
	set := map[int]bool{}
	for _, x := range a {
		set[x] = true
	}
	var out []int
	for _, y := range b {
		if set[y] {
			out = append(out, y)
		}
	}
	return out
}

// BuildPreflightReport is pure: blocking conditions stop the migration;
// warnings require an explicit operator ack but do not block.
func BuildPreflightReport(in PreflightInput) PreflightReport {
	var r PreflightReport
	block := func(code, msg string) { r.Blocks = append(r.Blocks, PreflightFinding{code, msg}) }
	warn := func(code, msg string) { r.Warnings = append(r.Warnings, PreflightFinding{code, msg}) }

	if in.SourceNodeID == in.TargetNodeID {
		block("same-node", "source and target are the same node")
	}
	if in.SourceArch != "" && in.TargetArch != "" && in.SourceArch != in.TargetArch {
		block("arch-mismatch", "source ("+in.SourceArch+") and target ("+in.TargetArch+") architectures differ")
	}
	if in.EstimatedBytes > 0 && in.TargetFreeBytes > 0 && in.TargetFreeBytes < in.EstimatedBytes {
		block("insufficient-disk", "target free space is less than the estimated migration size")
	}
	if c := intersect(in.StackPorts, in.TargetPortsInUse); len(c) > 0 && in.Disposition != DispositionClone {
		block("port-conflict", "target already uses one or more of the stack's host ports")
	} else if len(c) > 0 {
		warn("port-conflict", "clone: target uses some host ports; remap before starting the clone")
	}
	if in.TargetStackExists && !in.OverwriteAcked {
		block("stack-exists", "a stack with this id already exists on the target (ack overwrite to proceed)")
	}
	if in.HasSystemBind {
		warn("system-bind", "stack uses host/system bind mounts (e.g. docker.sock); these are not copied and must exist on the target")
	}
	if in.HasExternalVolume {
		warn("external-volume", "stack references external volumes; their data is not copied")
	}
	if in.HasDevice {
		warn("device-required", "stack requests devices/GPU; the target must have equivalent hardware")
	}
	return r
}
