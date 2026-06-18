package compose

// PreflightInput are the facts the orchestrator gathers (target queried over the
// cluster) and feeds to the pure report builder.
type PreflightInput struct {
	SourceNodeID, TargetNodeID string
	SourceArch, TargetArch     string
	// TargetFreeBytes is free space on the target's stacks FS; EstimatedBytes is
	// what lands there (stack files + bind data + headroom). TargetDockerFreeBytes
	// is free on the target's docker storage FS; EstimatedDockerBytes is what
	// lands there (volume + image data). TargetSameDevice is true when both roots
	// are on one filesystem, so the combined total must fit a single free figure.
	TargetFreeBytes       int64
	EstimatedBytes        int64
	TargetDockerFreeBytes int64
	EstimatedDockerBytes  int64
	TargetSameDevice      bool
	StackPorts            []int
	TargetPortsInUse      []int
	TargetStackExists     bool
	OverwriteAcked        bool
	HasSystemBind         bool
	HasExternalVolume     bool
	HasAbsBind            bool
	HasDevice             bool
	Disposition           Disposition
}

// largeTransferThreshold is the estimated size above which the operator is
// warned that the migration will move a lot of data and may run for a while.
const largeTransferThreshold int64 = 5 << 30 // 5 GiB

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
	// Non-nil slices so JSON emits [] not null — the UI's "no blocks && no
	// warnings" success check relies on length, not presence.
	r := PreflightReport{Blocks: []PreflightFinding{}, Warnings: []PreflightFinding{}}
	block := func(code, msg string) { r.Blocks = append(r.Blocks, PreflightFinding{code, msg}) }
	warn := func(code, msg string) { r.Warnings = append(r.Warnings, PreflightFinding{code, msg}) }

	if in.SourceNodeID == in.TargetNodeID {
		block("same-node", "source and target are the same node")
	}
	if in.SourceArch != "" && in.TargetArch != "" && in.SourceArch != in.TargetArch {
		block("arch-mismatch", "source ("+in.SourceArch+") and target ("+in.TargetArch+") architectures differ")
	}
	// Disk: volume + image data lands on the docker storage FS; stack files + bind
	// data on the stacks FS. When both roots share one device the combined total
	// must fit; when they differ each portion is checked against its own free space.
	if in.TargetSameDevice {
		total := in.EstimatedBytes + in.EstimatedDockerBytes
		if total > 0 && in.TargetFreeBytes > 0 && in.TargetFreeBytes < total {
			block("insufficient-disk", "target free space is less than the estimated migration size")
		}
	} else {
		if in.EstimatedBytes > 0 && in.TargetFreeBytes > 0 && in.TargetFreeBytes < in.EstimatedBytes {
			block("insufficient-disk", "target stacks filesystem free space is less than the stack files + bind data size")
		}
		if in.EstimatedDockerBytes > 0 && in.TargetDockerFreeBytes > 0 && in.TargetDockerFreeBytes < in.EstimatedDockerBytes {
			block("insufficient-disk-docker", "target docker storage free space is less than the volume + image data size")
		}
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
		warn("system-bind", "stack uses host/system bind mounts (e.g. docker.sock, /dev); these are NOT copied and must exist on the target")
	}
	if in.HasExternalVolume {
		warn("external-volume", "stack references external volumes; their data WILL be copied, which may duplicate data shared with other stacks")
	}
	if in.HasAbsBind {
		warn("absolute-bind-write", "stack has absolute-path bind mounts; their data will be written to the SAME absolute paths on the target (protected system paths are skipped)")
	}
	if in.EstimatedBytes+in.EstimatedDockerBytes > largeTransferThreshold {
		warn("large-transfer", "this migration moves a large amount of data and may take a while")
	}
	if in.HasDevice {
		warn("device-required", "stack requests devices/GPU; the target must have equivalent hardware")
	}
	return r
}
