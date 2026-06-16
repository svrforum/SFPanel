package compose

// Disposition is what happens to the SOURCE stack after a successful migration.
type Disposition string

const (
	DispositionRetain Disposition = "retain" // stop source, keep files/volumes
	DispositionDelete Disposition = "delete" // remove source after target verified healthy
	DispositionClone  Disposition = "clone"  // restart source; both run
)

func (d Disposition) Valid() bool {
	switch d {
	case DispositionRetain, DispositionDelete, DispositionClone:
		return true
	}
	return false
}

// Migration job phases (also the SSE event names).
const (
	PhasePreflight   = "preflight"
	PhaseQuiesce     = "quiesce"
	PhasePackage     = "package"
	PhaseTransfer    = "transfer"
	PhaseRestore     = "restore"
	PhaseUp          = "up"
	PhaseHealthcheck = "healthcheck"
	PhaseFinalize    = "finalize"
	PhaseRollback    = "rollback"
	PhaseDone        = "done"
	PhaseError       = "error"
)

type NodeRef struct {
	NodeID string `json:"nodeId"`
	Arch   string `json:"arch"`
}

// MountSpec describes one bind mount referenced by the stack (M2 populates data).
type MountSpec struct {
	Host    string `json:"host"`
	Kind    string `json:"kind"` // "in-stack" | "abs" | "system"
	Copy    bool   `json:"copy"`
	Archive string `json:"archive,omitempty"`
}

// VolumeSpec describes one named volume (M3 populates data).
type VolumeSpec struct {
	Compose  string `json:"compose"`
	Docker   string `json:"docker"`
	External bool   `json:"external"`
	Bytes    int64  `json:"bytes,omitempty"`
	Copy     bool   `json:"copy"`
	Archive  string `json:"archive,omitempty"`
}

// ImageSpec describes one image (M3 populates save/load).
type ImageSpec struct {
	Ref      string `json:"ref"`
	Pullable bool   `json:"pullable"`
	SaveLoad bool   `json:"saveLoad"`
	Archive  string `json:"archive,omitempty"`
}

// MigrationManifest is the single source of truth carried inside the bundle.
type MigrationManifest struct {
	SchemaVersion      int          `json:"schemaVersion"`
	StackID            string       `json:"stackId"`
	ComposeProjectName string       `json:"composeProjectName"`
	Source             NodeRef      `json:"source"`
	Target             NodeRef      `json:"target"`
	ComposeFile        string       `json:"composeFile"`
	HasEnv             bool         `json:"hasEnv"`
	ExtraFiles         []string     `json:"extraFiles,omitempty"`
	Binds              []MountSpec  `json:"binds,omitempty"`
	Volumes            []VolumeSpec `json:"volumes,omitempty"`
	Images             []ImageSpec  `json:"images,omitempty"`
	Ports              []int        `json:"ports,omitempty"`
	Devices            []string     `json:"devices,omitempty"`
	Disposition        Disposition  `json:"disposition"`
	// Overwrite carries the source operator's acked intent to replace a stack
	// that already exists on the target. The target refuses (409) to overwrite
	// an existing stack unless this is set, and backs the prior tenant up so a
	// failed import can't destroy it.
	Overwrite bool `json:"overwrite,omitempty"`
}
