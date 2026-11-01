package response

// Error codes are coarse, stable categories that the frontend keys its i18n
// messages off of. Two rules govern them:
//
//  1. Code string values MUST be unique (enforced by TestErrorCodeValuesUnique
//     in errors_test.go) — a duplicate would make two failure modes
//     indistinguishable to the UI and to audit/log filters.
//  2. The HTTP status is the CALLER's choice, passed to response.Fail per call
//     site. A single code can legitimately map to several statuses depending
//     on context (e.g. ErrInvalidRequest → 400 normally, 409 on a conflict).
//     There is intentionally no central code→status table; pick the status
//     that fits the failure, and reuse an existing code rather than minting a
//     near-duplicate.

// Common error codes
const (
	ErrInvalidRequest   = "INVALID_REQUEST"
	ErrInvalidBody      = "INVALID_BODY"
	ErrMissingFields    = "MISSING_FIELDS"
	ErrNotFound         = "NOT_FOUND"
	ErrAlreadyExists    = "ALREADY_EXISTS"
	ErrInternalError    = "INTERNAL_ERROR"
	ErrDBError          = "DB_ERROR"
	ErrReadError        = "READ_ERROR"
	ErrWriteError       = "WRITE_ERROR"
	ErrDeleteError      = "DELETE_ERROR"
	ErrDirError         = "DIR_ERROR"
	ErrIOError          = "IO_ERROR"
	ErrEmptyContent     = "EMPTY_CONTENT"
	ErrInvalidValue     = "INVALID_VALUE"
	ErrInvalidName      = "INVALID_NAME"
	ErrInvalidID        = "INVALID_ID"
	ErrInvalidAction    = "INVALID_ACTION"
	ErrInvalidPath      = "INVALID_PATH"
	ErrMissingPath      = "MISSING_PATH"
	ErrPermissionDenied = "PERMISSION_DENIED"
	ErrSSEError         = "SSE_ERROR"
)

// Auth error codes
const (
	ErrInvalidCredentials = "INVALID_CREDENTIALS"
	ErrInvalidToken       = "INVALID_TOKEN"
	ErrMissingToken       = "MISSING_TOKEN"
	ErrTokenError         = "TOKEN_ERROR"
	ErrTOTPRequired       = "TOTP_REQUIRED"
	ErrTOTPError          = "TOTP_ERROR"
	ErrInvalidTOTP        = "INVALID_TOTP"
	ErrInvalidPassword    = "INVALID_PASSWORD"
	ErrWeakPassword       = "WEAK_PASSWORD"
	ErrHashError          = "HASH_ERROR"
	ErrAlreadySetup       = "ALREADY_SETUP"
	ErrNoUser             = "NO_USER"
	ErrUserNotFound       = "USER_NOT_FOUND"
	ErrRateLimited        = "RATE_LIMITED"
	ErrCSRFTokenMissing   = "CSRF_TOKEN_MISSING"
	ErrCSRFTokenMismatch  = "CSRF_TOKEN_MISMATCH"
)

// Docker error codes
const (
	ErrDockerError         = "DOCKER_ERROR"
	ErrDockerFirewallError = "DOCKER_FIREWALL_ERROR"
	ErrComposeError        = "COMPOSE_ERROR"
	ErrComposeRisky        = "COMPOSE_RISKY"
	ErrComposeForbidden    = "COMPOSE_FORBIDDEN"
	ErrInvalidYAML         = "INVALID_YAML"
	ErrGitAuthFailed       = "GIT_AUTH_FAILED"
	ErrGitRepoNotFound     = "GIT_REPO_NOT_FOUND"
	ErrGitPathNotFound     = "GIT_PATH_NOT_FOUND"
	ErrGitCloneFailed      = "GIT_CLONE_FAILED"
	// ErrGitRateLimited is returned (HTTP 429) when GitHub refused the call
	// because the hourly allowance of this address or token is used up, not
	// because the token was refused. Details: reset_at (RFC 3339, UTC),
	// limit, retry_after (seconds), each when GitHub said it.
	ErrGitRateLimited     = "GIT_RATE_LIMITED"
	ErrStackAlreadyExists = "STACK_ALREADY_EXISTS"
)

// File error codes
const (
	ErrFileError       = "FILE_ERROR"
	ErrFileNotFound    = "FILE_NOT_FOUND"
	ErrFileDeleteError = "FILE_DELETE_ERROR"
	ErrFileWriteError  = "FILE_WRITE_ERROR"
	ErrFileTooLarge    = "FILE_TOO_LARGE"
	ErrMissingFile     = "MISSING_FILE"
	ErrNotAFile        = "NOT_A_FILE"
	ErrIsDirectory     = "IS_DIRECTORY"
	ErrInvalidFilename = "INVALID_FILENAME"
	ErrCriticalPath    = "CRITICAL_PATH"
	ErrReadProtected   = "READ_PROTECTED"
)

// System / Process error codes
const (
	ErrCommandFailed    = "COMMAND_FAILED"
	ErrServiceError     = "SERVICE_ERROR"
	ErrProcessError     = "PROCESS_ERROR"
	ErrProcessNotFound  = "PROCESS_NOT_FOUND"
	ErrKillFailed       = "KILL_FAILED"
	ErrInvalidPID       = "INVALID_PID"
	ErrInvalidSignal    = "INVALID_SIGNAL"
	ErrHostInfoError    = "HOST_INFO_ERROR"
	ErrMetricsError     = "METRICS_ERROR"
	ErrUsageError       = "USAGE_ERROR"
	ErrToolNotInstalled = "TOOL_NOT_INSTALLED"

	// ErrServiceProtected is returned (HTTP 403) when stopping, restarting or
	// disabling a unit would take the host off the network or reset it:
	// networking on an ifupdown host, corosync or watchdog-mux on Proxmox VE.
	// Details: unit, and hint naming the safe way when there is one.
	ErrServiceProtected = "SERVICE_PROTECTED"

	// ErrServiceConfirmRequired is returned (HTTP 409) for a unit the
	// operator may stop, but only after typing its name (?confirm=<unit>).
	// Details: unit.
	ErrServiceConfirmRequired = "SERVICE_CONFIRM_REQUIRED"

	// ErrProcessProtected is returned (HTTP 403) for a signal to a process
	// whose death resets or partitions a Proxmox node (watchdog-mux,
	// corosync, pmxcfs, pve-ha-*). Details: name.
	ErrProcessProtected = "PROCESS_PROTECTED"

	// ErrProcessIsGuest is returned (HTTP 409) for a signal to a virtual
	// machine's process: killing it pulls the VM's power. Details: vmid.
	ErrProcessIsGuest = "PROCESS_IS_GUEST"
)

// AI workspace error codes (internal/feature/ai)
const (
	ErrInvalidAccount    = "INVALID_ACCOUNT"      // run_as not in the allowlist (400)
	ErrInvalidTool       = "INVALID_TOOL"         // tool not claude|codex|gemini|shell (400)
	ErrTmuxMissing       = "TMUX_MISSING"         // tmux not installed on this node (503)
	ErrAISessionNotFound = "AI_SESSION_NOT_FOUND" // no such row (404)
	ErrAISessionLimit    = "AI_SESSION_LIMIT"     // 20 live sessions already (409)
	ErrAISessionState    = "AI_SESSION_STATE"     // rerun/restart in the wrong state (409)
	ErrAIProfileExists   = "AI_PROFILE_EXISTS"    // a directory of that name is already there (409)
	ErrAIProfileInUse    = "AI_PROFILE_IN_USE"    // a live session is using it (409)
	// AI_ prefixed, like the two codes above and unlike the three
	// generically-named ones at the top of this block: a launch option is an
	// AI-session concept and nothing outside internal/feature/ai can emit
	// this. The unprefixed spelling LAUNCH_ROOT_DANGER was never released, so
	// this is the name the wire freezes on.
	ErrAILaunchRootDanger = "AI_LAUNCH_ROOT_DANGER" // the tool refuses its bypass flag as root (400)
)

// Package (APT) error codes
const (
	ErrAPTError           = "APT_ERROR"
	ErrAPTUpdateError     = "APT_UPDATE_ERROR"
	ErrAPTInstallError    = "APT_INSTALL_ERROR"
	ErrAPTRemoveError     = "APT_REMOVE_ERROR"
	ErrAPTSearchError     = "APT_SEARCH_ERROR"
	ErrAPTUpgradeError    = "APT_UPGRADE_ERROR"
	ErrInstallError       = "INSTALL_ERROR"
	ErrInvalidPackageName = "INVALID_PACKAGE_NAME"

	// ErrAPTRepoUnauthorized names a repository that refused apt (401), as
	// Proxmox's enterprise repository does without a subscription. Usually a
	// warning beside a result: the other repositories were refreshed.
	// Details: uri.
	ErrAPTRepoUnauthorized = "APT_REPO_UNAUTHORIZED"

	// ErrAPTRepoFailed names a repository apt could not fetch for another
	// reason (down, unsigned). A warning, like ErrAPTRepoUnauthorized.
	// Details: uri.
	ErrAPTRepoFailed = "APT_REPO_FAILED"

	// ErrDockerOnPVE is returned (HTTP 409) when Docker is to be installed
	// on a Proxmox VE host. Docker changes the host's forwarding and bridge
	// filtering under every guest; Proxmox recommends it in a VM or container.
	ErrDockerOnPVE = "DOCKER_ON_PVE"

	// ErrAPTTransactionRunning is returned (HTTP 409) for an apt operation
	// while a package transaction (an upgrade, or an install from the
	// Packages page) runs in its unit. Details: id, kind — the page offers to
	// follow it.
	ErrAPTTransactionRunning = "APT_TRANSACTION_RUNNING"
)

// Firewall error codes
const (
	ErrFirewallError   = "FIREWALL_ERROR"
	ErrUFWError        = "UFW_ERROR"
	ErrUFWEnableError  = "UFW_ENABLE_ERROR"
	ErrUFWDisableError = "UFW_DISABLE_ERROR"
	ErrUFWAddRuleError = "UFW_ADD_RULE_ERROR"
	ErrUFWDeleteError  = "UFW_DELETE_ERROR"

	// ErrUFWLockout is returned (HTTP 409) when enabling UFW, adding a rule or
	// deleting one would cut off SSH (22) and the panel port, the Proxmox web
	// UI (8006) on a Proxmox VE host, or a port the rest of the cluster
	// reaches the node on; ?force=true proceeds anyway. Its own code so the
	// page can say so in its language; the last two name their ports in the
	// details ("port", "cluster_ports").
	ErrUFWLockout         = "UFW_LOCKOUT"
	ErrIPTablesError      = "IPTABLES_ERROR"
	ErrFail2banError      = "FAIL2BAN_ERROR"
	ErrFail2banInstall    = "FAIL2BAN_INSTALL_ERROR"
	ErrFail2banStart      = "FAIL2BAN_START_ERROR"
	ErrFail2banJail       = "FAIL2BAN_JAIL_ERROR"
	ErrFail2banEnable     = "FAIL2BAN_ENABLE_ERROR"
	ErrFail2banDisable    = "FAIL2BAN_DISABLE_ERROR"
	ErrFail2banConfig     = "FAIL2BAN_CONFIG_ERROR"
	ErrFail2banUnban      = "FAIL2BAN_UNBAN_ERROR"
	ErrFail2banReload     = "FAIL2BAN_RELOAD_ERROR"
	ErrInvalidPort        = "INVALID_PORT"
	ErrInvalidProtocol    = "INVALID_PROTOCOL"
	ErrInvalidIP          = "INVALID_IP"
	ErrInvalidRuleNumber  = "INVALID_RULE_NUMBER"
	ErrInvalidFromAddress = "INVALID_FROM_ADDRESS"
	ErrInvalidToAddress   = "INVALID_TO_ADDRESS"
	ErrInvalidJailName    = "INVALID_JAIL_NAME"
	ErrMissingJailName    = "MISSING_JAIL_NAME"
	ErrJailExists         = "JAIL_EXISTS"
	ErrInvalidBanTime     = "INVALID_BAN_TIME"
	ErrInvalidFindTime    = "INVALID_FIND_TIME"
	ErrInvalidMaxRetry    = "INVALID_MAX_RETRY"
	ErrInvalidLogPath     = "INVALID_LOG_PATH"
	ErrInvalidFilterName  = "INVALID_FILTER_NAME"
	ErrUnknownTemplate    = "UNKNOWN_TEMPLATE"
	ErrEnableFailed       = "ENABLE_FAILED"
	ErrDisableFailed      = "DISABLE_FAILED"
	ErrStartFailed        = "START_FAILED"
	ErrStopFailed         = "STOP_FAILED"
	ErrRestartFailed      = "RESTART_FAILED"

	// ErrFirewallManagedByPVE is returned (HTTP 409) for a ufw change on a
	// Proxmox VE host, whose firewall is pve-firewall: a ufw default-deny
	// next to it cuts the web UI on 8006, cluster traffic and guest consoles.
	ErrFirewallManagedByPVE = "FIREWALL_MANAGED_BY_PVE"
)

// Disk error codes
const (
	ErrDiskError         = "DISK_ERROR"
	ErrLVMError          = "LVM_ERROR"
	ErrRAIDError         = "RAID_ERROR"
	ErrSwapError         = "SWAP_ERROR"
	ErrSMARTError        = "SMART_ERROR"
	ErrPartitionError    = "PARTITION_ERROR"
	ErrFSError           = "FS_ERROR"
	ErrFormatError       = "FORMAT_ERROR"
	ErrMountError        = "MOUNT_ERROR"
	ErrUnmountError      = "UNMOUNT_ERROR"
	ErrResizeError       = "RESIZE_ERROR"
	ErrExpandError       = "EXPAND_ERROR"
	ErrInvalidDevice     = "INVALID_DEVICE"
	ErrInvalidPartition  = "INVALID_PARTITION"
	ErrInvalidFSType     = "INVALID_FSTYPE"
	ErrInvalidMountpoint = "INVALID_MOUNTPOINT"
	ErrInvalidOptions    = "INVALID_OPTIONS"
	ErrInvalidSize       = "INVALID_SIZE"
	ErrInvalidStart      = "INVALID_START"
	ErrInvalidEnd        = "INVALID_END"
	ErrInvalidVG         = "INVALID_VG"
	ErrInvalidLevel      = "INVALID_LEVEL"

	// ErrSMARTTestInProgress is returned (HTTP 409) by a self-test request while
	// the device is still running one; it cannot start until that one ends.
	ErrSMARTTestInProgress = "SMART_TEST_IN_PROGRESS"

	// ErrDeviceInUse is returned (HTTP 409) when the kernel holds a partition
	// or disk — mounted, active swap, an LVM physical volume, a RAID member or
	// a ZFS vdev — so it cannot be formatted or deleted until released.
	ErrDeviceInUse = "DEVICE_IN_USE"

	// ErrDiskNoPartitionTable is returned (HTTP 409) when a partition is asked
	// for on a disk without a partition table, as a new disk arrives.
	ErrDiskNoPartitionTable = "DISK_NO_PARTITION_TABLE"

	// ErrDiskNotEmpty is returned (HTTP 409) when a partition table is asked
	// for on a disk that already has one, or a filesystem, LVM, RAID or ZFS
	// signature written across the whole disk.
	ErrDiskNotEmpty = "DISK_NOT_EMPTY"

	// ErrDiskGuestVolume is returned (HTTP 409) for a change to a device that
	// belongs to a virtual machine or container: a zvol, a guest LV, a disk a
	// QEMU process has open. Details: device, owner.
	ErrDiskGuestVolume = "DISK_GUEST_VOLUME"

	// ErrDiskBootPartition is returned (HTTP 409) for a change to an EFI
	// System or BIOS boot partition; Proxmox keeps its ESPs unmounted, so no
	// mount check sees them. Details: device.
	ErrDiskBootPartition = "DISK_BOOT_PARTITION"

	// ErrLVHasDependents is returned (HTTP 409) for removing a thin pool that
	// still holds thin volumes. Details: lv.
	ErrLVHasDependents = "LV_HAS_DEPENDENTS"

	// ErrLVShrinkConfirmRequired is returned (HTTP 409) for a resize that
	// would make a logical volume smaller without the explicit shrink flag.
	// Details: lv.
	ErrLVShrinkConfirmRequired = "LV_SHRINK_CONFIRM_REQUIRED"

	// ErrSwapUnsupportedFS is returned (HTTP 400) for a swap file on a file
	// system that cannot hold one, such as ZFS. Details: fs.
	ErrSwapUnsupportedFS = "SWAP_UNSUPPORTED_FS"

	// ErrPartitionNotFormatted is returned (HTTP 500) when a partition asked
	// for with format was created but could not be formatted: the partition
	// is there, empty, and the page should list it.
	ErrPartitionNotFormatted = "PARTITION_NOT_FORMATTED"

	// Network shares (SMB/NFS).
	ErrShareToolMissing = "SHARE_TOOL_MISSING"
	ErrShareUnreachable = "SHARE_UNREACHABLE"
	ErrShareExists      = "SHARE_EXISTS"
	ErrShareNotManaged  = "SHARE_NOT_MANAGED"
	ErrFstabWrite       = "FSTAB_WRITE_ERROR"
)

// Network error codes
const (
	ErrNetworkError = "NETWORK_ERROR"
	ErrNetplanError = "NETPLAN_ERROR"
	ErrSSError      = "SS_ERROR"

	// ErrNetworkReadOnly is returned (HTTP 409) for a network change on a
	// host whose network the panel does not write: anything but netplan,
	// and on Proxmox VE the bridges every guest hangs off. Details: stack.
	ErrNetworkReadOnly = "NETWORK_READ_ONLY"
)

// WireGuard error codes
const (
	ErrWireGuardError = "WIREGUARD_ERROR"
	ErrWGListError    = "WG_LIST_ERROR"
	ErrWGInstallError = "WG_INSTALL_ERROR"
	ErrWGUpError      = "WG_UP_ERROR"
	ErrWGDownError    = "WG_DOWN_ERROR"

	// ErrWGResolvconfMissing is returned (HTTP 409) when a configuration sets
	// DNS = and the host has no resolvconf provider for wg-quick to hand it
	// to; wg-quick would fail and remove the interface. Details: iface.
	ErrWGResolvconfMissing = "WG_RESOLVCONF_MISSING"
)

// Tailscale error codes
const (
	ErrTailscaleError = "TAILSCALE_ERROR"
	ErrTSUpError      = "TS_UP_ERROR"
	ErrTSDownError    = "TS_DOWN_ERROR"
	ErrTSLogoutError  = "TS_LOGOUT_ERROR"
	ErrTSPeersError   = "TS_PEERS_ERROR"
	ErrTSSetError     = "TS_SET_ERROR"

	// ErrTailscaleRouteOverlap is returned (HTTP 409) when accepting a peer's
	// routes would send a network this host is directly on into the tunnel,
	// cutting off its replies to that LAN. Details: prefix, peer.
	ErrTailscaleRouteOverlap = "TAILSCALE_ROUTE_OVERLAP"
)

// Log error codes
const (
	ErrLogError       = "LOG_ERROR"
	ErrLogNotFound    = "LOG_NOT_FOUND"
	ErrInvalidLines   = "INVALID_LINES"
	ErrInvalidSource  = "INVALID_SOURCE"
	ErrMissingSource  = "MISSING_SOURCE"
	ErrMissingQuery   = "MISSING_QUERY"
	ErrInvalidQuery   = "INVALID_QUERY"
	ErrSourceExists   = "SOURCE_EXISTS"
	ErrInvalidComment = "INVALID_COMMENT"
)

// Cron error codes
const (
	ErrCronError       = "CRON_ERROR"
	ErrInvalidSchedule = "INVALID_SCHEDULE"
	ErrCronConflict    = "CRON_CONFLICT" // line at the given index no longer matches expected_raw
)

// Settings error codes
const (
	ErrEmptySettings = "EMPTY_SETTINGS"
)

// Tuning error codes
const (
	ErrTuningError = "TUNING_ERROR"
)

// App Store error codes
const (
	ErrAppStoreError     = "APPSTORE_ERROR"
	ErrPortConflict      = "PORT_CONFLICT"
	ErrContainerConflict = "CONTAINER_CONFLICT"
)

// Alert error codes
const (
	ErrAlertError   = "ALERT_ERROR"
	ErrChannelError = "CHANNEL_ERROR"
	ErrRuleError    = "RULE_ERROR"

	// ErrAlertRuleTypeNotEvaluated is returned (HTTP 400) for an alert rule
	// whose type nothing evaluates: creating one, changing a rule to one, or
	// changing one that is already stored. Such a rule is saved and never
	// fires (#132). Details: type.
	ErrAlertRuleTypeNotEvaluated = "ALERT_RULE_TYPE_NOT_EVALUATED"
)

// System update error codes
const (
	ErrUpdateCheckFailed = "UPDATE_CHECK_FAILED"
	ErrUpdateFailed      = "UPDATE_FAILED"
	ErrUpdateDowngrade   = "UPDATE_DOWNGRADE_BLOCKED"
	ErrUpdateInProgress  = "UPDATE_IN_PROGRESS"
	ErrBackupFailed      = "BACKUP_FAILED"
	ErrRestoreFailed     = "RESTORE_FAILED"
	// ErrRestoreManagerBackupOnAgent is returned (HTTP 400) when an agent is
	// given a cluster manager's backup: it holds the cluster's admin account
	// and JWT key, which an agent must not hold. The archive itself is sound.
	ErrRestoreManagerBackupOnAgent = "RESTORE_MANAGER_BACKUP_ON_AGENT"
	// ErrRestoreAgentBackupOnManager is returned (HTTP 400) when a manager or
	// standalone panel is given an agent's backup, which would start it as
	// that agent without the keys only that agent has.
	ErrRestoreAgentBackupOnManager = "RESTORE_AGENT_BACKUP_ON_MANAGER"
)

// Command execution error codes
const (
	ErrCommandTimeout = "COMMAND_TIMEOUT"
)

// Cluster error codes
const (
	// ErrClusterQuorum is returned (HTTP 409) when an operation would drop the
	// surviving voters below Raft quorum — e.g. leaving a cluster whose only
	// other voter is unreachable. Callers pass http.StatusConflict explicitly.
	ErrClusterQuorum = "CLUSTER_QUORUM"
	// ErrClusterNotRunning is returned (HTTP 409) by init and join on a node
	// whose config says it belongs to a cluster while its cluster service is
	// not running — the start failed at boot. Either operation would replace
	// or delete the membership still on disk.
	ErrClusterNotRunning = "CLUSTER_NOT_RUNNING"
	// ErrClusterTokenKind is the cluster handlers' code (HTTP 400) for
	// cluster.ErrTokenKind: a join token offered for another kind of node than
	// the one it was minted for. The refusal leaves it usable for that kind.
	ErrClusterTokenKind = "CLUSTER_TOKEN_KIND"
	// ErrClusterMaxAgents is returned (HTTP 400) when an agent join token is
	// asked for and the cluster already has cluster.max_agents agents (default
	// 100). The manager cap keeps its generic ErrInvalidRequest.
	ErrClusterMaxAgents = "CLUSTER_MAX_AGENTS"
	// ErrClusterRouteNotOnAgent is returned (HTTP 409) for a /cluster/ route
	// whose ?node= names an agent. Cluster management runs on managers, and
	// the manager does not run it in the agent's place.
	ErrClusterRouteNotOnAgent = "CLUSTER_ROUTE_NOT_ON_AGENT"
	// ErrClusterAgentAuthFailed is returned (HTTP 502) when a relay to an agent
	// cannot be signed — replicated state holds no key for it — or the agent
	// answers 401. Passed on as a 401, the browser would end the operator's
	// session on this manager.
	ErrClusterAgentAuthFailed = "CLUSTER_AGENT_AUTH_FAILED"
	// ErrClusterAgentInsecure is returned (HTTP 502) when a relay to an agent
	// would go over plain HTTP: its panel CA is not in replicated state.
	ErrClusterAgentInsecure = "CLUSTER_AGENT_PANEL_INSECURE"
	// ErrClusterNodeUnreachable is returned (HTTP 502) for a ?node= request
	// to an agent this manager's last probe, within three minutes, could not
	// reach: refused at once rather than left to time out on a dial. Details:
	// name (the node's name, or its ID when it has none).
	ErrClusterNodeUnreachable = "CLUSTER_NODE_UNREACHABLE"
	// ErrClusterConvertRefused is returned when a manager is not converted to
	// an agent, nothing having changed: HTTP 409 for a gate the leader or the
	// node refuses on (the leader itself, quorum, readiness, the name or
	// address an agent would need), 502 when the node refused the request.
	// The message names the reason.
	ErrClusterConvertRefused = "CLUSTER_CONVERT_REFUSED"
	// ErrClusterPromoteRefused is returned when an agent is not promoted to a
	// manager, nothing having changed: HTTP 409 for a gate the leader or the
	// agent refuses on (a node that is not an agent, one not online, no room
	// for another manager, another promotion under way), 502 when the agent
	// refused the request. The message names the reason.
	ErrClusterPromoteRefused = "CLUSTER_PROMOTE_REFUSED"
	// ErrClusterRotationRefused is returned (HTTP 409) when a secret rotation
	// is not started, or a CA rotation not stopped, nothing having changed:
	// a node that cannot follow, one offline, a rotation already running, or
	// past the point a CA rotation can be undone. The message names it.
	ErrClusterRotationRefused = "CLUSTER_ROTATION_REFUSED"
	// ErrClusterCARotating is returned (HTTP 409) for an operation refused
	// because the cluster CA is being replaced: a join token, whose
	// fingerprint would name a CA the rotation is about to change, or a
	// cluster update, which restarts nodes under it.
	ErrClusterCARotating = "CLUSTER_CA_ROTATING"
	// ErrClusterLeaveUnacknowledged is a warning code, never an error: a
	// leave that went ahead while no leader took this node out, so the others
	// still list it until it is removed on the leader. Details: node_id.
	ErrClusterLeaveUnacknowledged = "CLUSTER_LEAVE_UNACKNOWLEDGED"
	// ErrClusterAgentsNotReached is a warning code, never an error: a disband
	// that went ahead without telling these agents, each of which needs
	// 'sudo sfpanel agent reset --yes' on its own server. Details: agents
	// (their names, comma-separated).
	ErrClusterAgentsNotReached = "CLUSTER_AGENTS_NOT_REACHED"
	// ErrClusterManagerBackups is a warning code, never an error: a manager
	// being converted to an agent has scheduled backups from its manager days,
	// which hold the cluster's secrets until they are rotated.
	ErrClusterManagerBackups = "CLUSTER_MANAGER_BACKUPS"
)

// TLS error codes
const (
	// ErrTLSDisabled is returned (HTTP 404) when a caller asks for panel
	// certificate material on a panel that serves plain HTTP. There is nothing
	// to hand out, and it is not an error on the caller's part — the feature is
	// simply not turned on.
	ErrTLSDisabled = "TLS_DISABLED"
	// ErrTLSUnavailable is returned (HTTP 500) when TLS is enabled but the
	// certificate material cannot be read.
	ErrTLSUnavailable = "TLS_UNAVAILABLE"
)

// Task job error codes: a node's half of a group task, which the leader's
// engine calls.
const (
	// ErrTaskJobClusterOnly is returned (HTTP 403) for a /task-jobs request
	// that is not a signed cluster call, and by a manager asked to relay one
	// with ?node=: a browser reaching a node's jobs directly would run a step
	// past the engine's gates and its audit.
	ErrTaskJobClusterOnly = "TASK_JOB_CLUSTER_ONLY"
	// ErrTaskNodeBusy is returned (HTTP 409) when a node is asked to start a
	// job while another is running or waiting on its reboot. Details: job_id,
	// run_id and task of the job it is busy with.
	ErrTaskNodeBusy = "TASK_NODE_BUSY"
	// ErrTaskJobNotFound is returned (HTTP 404) for a job ID the node has no
	// record of: never started here, or swept.
	ErrTaskJobNotFound = "TASK_JOB_NOT_FOUND"
	// ErrTaskJobRunning is returned (HTTP 409) when a running job is asked to
	// stop without force: by default a job runs to its end, so an apt run is
	// never cut off half-way.
	ErrTaskJobRunning = "TASK_JOB_RUNNING"
	// ErrTaskRebootPVEHost is returned (HTTP 409) when a reboot step is sent
	// to a Proxmox VE host: the panel does not know the guests running on it,
	// so it does not reboot one. The engine shows the step as skipped.
	ErrTaskRebootPVEHost = "TASK_REBOOT_PVE_HOST"
	// ErrTaskRebootUnsupported is returned (HTTP 409) when a reboot step is
	// sent to a node without systemd, which has nothing to schedule it with.
	ErrTaskRebootUnsupported = "TASK_REBOOT_UNSUPPORTED"
	// ErrTaskRunActive is returned (HTTP 409) when a cluster update, a CA
	// rotation, a conversion or a promotion is asked for while a group task
	// run is under way: each restarts or re-keys nodes under the run's steps.
	// Details: run_id of the run it waits for.
	ErrTaskRunActive = "TASK_RUN_ACTIVE"
	// ErrAgentInstallActive is returned (HTTP 409) when a manual leadership
	// transfer, or a group task run's start or rerun, is asked for while an
	// agent install is under way: the install's sessions, credentials and
	// tokens live on the leader that started it, and a task run hands the
	// leadership over when its turn comes to the leader.
	ErrAgentInstallActive = "AGENT_INSTALL_ACTIVE"
	// ErrAgentInstallNotRerunnable is returned (HTTP 409) when an agent
	// install's run is asked to run again as a task: the credentials are
	// gone with it, so another install is started from its form.
	ErrAgentInstallNotRerunnable = "AGENT_INSTALL_NOT_RERUNNABLE"
	// ErrAgentInstallLogGone is returned (HTTP 404) for the output of an
	// agent install's phase that no session on the leader holds any more:
	// it lives in the memory of the leader that ran the install, and goes
	// with it.
	ErrAgentInstallLogGone = "AGENT_INSTALL_LOG_GONE"
	// ErrAgentInstallHostKeyChanged is returned (HTTP 409) when a server's
	// host key is approved with a fingerprint other than the one the row
	// shows; details: host_key, the one the row shows. It is also the reason
	// of a row whose server presented another key at the login than the one
	// approved; details: host_key, the key the server presented (absent when
	// it offered none of the approved key's type), and approved, the one
	// approved.
	ErrAgentInstallHostKeyChanged = "AGENT_INSTALL_HOST_KEY_CHANGED"
	// ErrAgentInstallNotWaiting is returned (HTTP 409) for an answer to a
	// question a row is not asking (a host key, a standalone panel, the
	// review), or a recheck or revert of a row that has no registered agent
	// failing its checks.
	ErrAgentInstallNotWaiting = "AGENT_INSTALL_NOT_WAITING"
	// ErrAgentInstallUnreleased is returned (HTTP 409) when an install is
	// asked of a leader whose version is no release, without
	// SFPANEL_AGENT_ARCHIVES naming a directory of its archives and
	// checksums.txt. Details: version.
	ErrAgentInstallUnreleased = "AGENT_INSTALL_UNRELEASED"
	// ErrAgentInstallMixedVersions is returned (HTTP 409) when an install is
	// asked while the managers run different versions: the agent installs
	// at the leader's. Details: version, managers (name (version), …).
	ErrAgentInstallMixedVersions = "AGENT_INSTALL_MIXED_VERSIONS"
	// ErrAgentInstallNotFound is returned (HTTP 404) for an install run, or
	// a server of one, that no session on this leader holds.
	ErrAgentInstallNotFound = "AGENT_INSTALL_NOT_FOUND"
	// ErrAgentInstallInvalid is returned (HTTP 400) for an install request,
	// or an answer to one of its questions, that is not one: no servers or
	// too many, a port, name or group out of bounds, a host given twice, a
	// login method without its secret or with another method's. Details:
	// field, the request's own name for what is wrong, and row (s1, s2 …,
	// in the order the servers were given) when it is one server's.
	ErrAgentInstallInvalid = "AGENT_INSTALL_INVALID"
)

// Group task error codes: the task and task-run API on a manager.
const (
	// ErrTaskInvalid is returned (HTTP 400) for a task document with
	// problems. The response's data.problems lists each with its line.
	ErrTaskInvalid = "TASK_INVALID"
	// ErrTaskTooLarge is returned (HTTP 400) for a task document over 64 KiB.
	ErrTaskTooLarge = "TASK_TOO_LARGE"
	// ErrTaskNameTaken is returned (HTTP 409) when another task, stored or
	// built in, already has the name.
	ErrTaskNameTaken = "TASK_NAME_TAKEN"
	// ErrTaskNotFound is returned (HTTP 404) for a task ID that names no
	// stored or built-in task.
	ErrTaskNotFound = "TASK_NOT_FOUND"
	// ErrTaskRunNotFound is returned (HTTP 404) for a run ID that names no
	// run kept in the cluster's state.
	ErrTaskRunNotFound = "TASK_RUN_NOT_FOUND"
	// ErrTaskReadOnly is returned (HTTP 409) when a built-in task is asked
	// to change or go, or a task imported from Git to change.
	ErrTaskReadOnly = "TASK_READ_ONLY"
	// ErrTaskManagersNotReady is returned (HTTP 409) when a run is asked for
	// before every manager runs code that keeps the task store. Details:
	// managers, the ones it waits for with why, comma-separated.
	ErrTaskManagersNotReady = "TASK_MANAGERS_NOT_READY"
	// ErrTaskInputMissing is returned (HTTP 400) when a required input was
	// not given and has no default. Details: input.
	ErrTaskInputMissing = "TASK_INPUT_MISSING"
	// ErrTaskNoTargets is returned (HTTP 400) when a run would take no node.
	ErrTaskNoTargets = "TASK_NO_TARGETS"
	// ErrTaskTooManyTargets is returned (HTTP 400) when a run's targets come
	// to more than 200 nodes. Details: max.
	ErrTaskTooManyTargets = "TASK_TOO_MANY_TARGETS"
	// ErrTaskTargetBusy is returned (HTTP 409) when a node a run would take
	// is held by another run under way. Details: node_id, run_id.
	ErrTaskTargetBusy = "TASK_TARGET_BUSY"
	// ErrTaskPlanChanged is returned (HTTP 409) when a run is started with
	// confirmed steps and the task, its inputs or its targets are not the
	// ones its preview showed (plan_id): preview again.
	ErrTaskPlanChanged = "TASK_PLAN_CHANGED"
	// ErrTaskRebootBusy is returned (HTTP 409) when a run that reboots a
	// manager is asked for while another run under way does too: each judges
	// the managers' quorum from the managers online, so two at once could
	// both reboot one. Details: run_id of the other run.
	ErrTaskRebootBusy = "TASK_REBOOT_BUSY"
	// ErrTaskClusterChanging is returned (HTTP 409) when a run is asked for
	// while a manager is being converted to an agent or an agent promoted to
	// a manager. Details: what (conversion | promotion).
	ErrTaskClusterChanging = "TASK_CLUSTER_CHANGING"
)

// Agent install row reasons: why the installer stopped at one server of an
// install run. They are never an HTTP answer's code: a row of the run record
// carries one, with its details, and the screen words it.
const (
	// ErrAgentInstallOS is a server that is not Ubuntu or Debian. Details:
	// os_id, os_like.
	ErrAgentInstallOS = "AGENT_INSTALL_OS"
	// ErrAgentInstallArch is a CPU no release is built for (amd64 and arm64
	// are). Details: arch, as uname -m prints it.
	ErrAgentInstallArch = "AGENT_INSTALL_ARCH"
	// ErrAgentInstallNoSystemd is a server without systemd: install.sh would
	// install the binary and start nothing.
	ErrAgentInstallNoSystemd = "AGENT_INSTALL_NO_SYSTEMD"
	// ErrAgentInstallNotRoot is a login that cannot become root: not root,
	// and no sudo, or sudo that asks for a password none was given for.
	// Details: sudo (none | password | refused, sudo turning the password
	// given down).
	ErrAgentInstallNotRoot = "AGENT_INSTALL_NOT_ROOT"
	// ErrAgentInstallPortBusy is a panel or cluster port another program
	// listens on. Details: port, owner (a process name, or ? when unknown).
	ErrAgentInstallPortBusy = "AGENT_INSTALL_PORT_BUSY"
	// ErrAgentInstallClock is a server whose clock is more than 30 seconds
	// from the leader's: managers refuse its relays. Details: skew_seconds.
	ErrAgentInstallClock = "AGENT_INSTALL_CLOCK"
	// ErrAgentInstallCannotReachManager is a server that cannot open a TCP
	// connection to the leader's cluster port. Details: manager.
	ErrAgentInstallCannotReachManager = "AGENT_INSTALL_CANNOT_REACH_MANAGER"
	// ErrAgentInstallIPNotLocal is an IP the server does not hold: the agent
	// registers at it and listens on it. Details: ip.
	ErrAgentInstallIPNotLocal = "AGENT_INSTALL_IP_NOT_LOCAL"
	// ErrAgentInstallAlreadyAgent is a server that is an agent already.
	ErrAgentInstallAlreadyAgent = "AGENT_INSTALL_ALREADY_AGENT"
	// ErrAgentInstallAlreadyManager is a server that is a cluster manager.
	ErrAgentInstallAlreadyManager = "AGENT_INSTALL_ALREADY_MANAGER"
	// ErrAgentInstallNewerInstalled is a server whose SFPanel is newer than
	// the version being installed, or of a version that cannot be read:
	// install.sh would downgrade it. Details: installed, version.
	ErrAgentInstallNewerInstalled = "AGENT_INSTALL_NEWER_INSTALLED"
	// ErrAgentInstallManagerFiles is a server that still holds files a
	// manager keeps (the CA key, the Raft state, join tokens). Details: files,
	// comma-separated.
	ErrAgentInstallManagerFiles = "AGENT_INSTALL_MANAGER_FILES"
	// ErrAgentInstallOperatorTLS is a panel serving the operator's own
	// certificate (server.tls.cert_file or key_file set): the agent join
	// requires the panel's certificate to verify for 127.0.0.1 and the
	// advertise address, and refuses it only after install.sh has stopped
	// the panel. Details: ca_file (1 when server.tls.ca_file is set, else 0).
	ErrAgentInstallOperatorTLS = "AGENT_INSTALL_OPERATOR_TLS"
	// ErrAgentInstallNoTools is a server missing a command the install needs.
	// Details: missing, comma-separated.
	ErrAgentInstallNoTools = "AGENT_INSTALL_NO_TOOLS"
	// ErrAgentInstallNoInternet is a server asked to install online that
	// cannot reach GitHub's release downloads.
	ErrAgentInstallNoInternet = "AGENT_INSTALL_NO_INTERNET"
	// ErrAgentInstallNameInvalid is a node name the cluster refuses: the one
	// given, or the server's hostname when none was. Details: name or
	// hostname.
	ErrAgentInstallNameInvalid = "AGENT_INSTALL_NAME_INVALID"
	// ErrAgentInstallDisk is a server short of disk space for the binary and
	// its archive. Details: free_kb, need_kb.
	ErrAgentInstallDisk = "AGENT_INSTALL_DISK"
	// ErrAgentInstallAuthFailed is an SSH login the server refused.
	ErrAgentInstallAuthFailed = "AGENT_INSTALL_AUTH_FAILED"
	// ErrAgentInstallUnreachable is a server the leader cannot open SSH to.
	ErrAgentInstallUnreachable = "AGENT_INSTALL_UNREACHABLE"
	// ErrAgentInstallFailed is an install that failed for a reason with no
	// code of its own; the row's log says what happened.
	ErrAgentInstallFailed = "AGENT_INSTALL_FAILED"
	// ErrAgentInstallOffline is an agent that registered but did not come
	// online in time.
	ErrAgentInstallOffline = "AGENT_INSTALL_OFFLINE"
	// ErrAgentInstallLeaderChanged is a row cut off by a leader change: the
	// credentials and the token lived on the old leader only.
	ErrAgentInstallLeaderChanged = "AGENT_INSTALL_LEADER_CHANGED"
	// ErrAgentInstallCredentialsGone is a row whose credentials were wiped
	// before it was reverted: the node was removed, and what was installed on
	// the server is the operator's to clean up.
	ErrAgentInstallCredentialsGone = "AGENT_INSTALL_CREDENTIALS_GONE"
	// ErrAgentInstallClusterNotReady is a server the cluster would not let
	// register now. Details: gate (proxy_rotation | ca_rotation | managers |
	// max_agents | ca_key | other).
	ErrAgentInstallClusterNotReady = "AGENT_INSTALL_CLUSTER_NOT_READY"
	// ErrAgentInstallIdentityTaken is a node name or address a node of the
	// cluster has already, or another server of the same install. Details:
	// name and/or ip, and row, the other server's, when it is one.
	ErrAgentInstallIdentityTaken = "AGENT_INSTALL_IDENTITY_TAKEN"
	// ErrAgentInstallStopped is a row stopped by the operator after its
	// agent registered: the node stays in the cluster until the row is
	// reverted. Details: registered ("1").
	ErrAgentInstallStopped = "AGENT_INSTALL_STOPPED"
)

// File manager error codes
const (
	// ErrNotTextFile is returned (HTTP 415) when a caller asks to read a file
	// as text that is not text. Answering with the bytes anyway is what
	// corrupted files: Go replaces invalid UTF-8 with U+FFFD, the editor shows
	// mojibake with Save enabled, and saving writes the replacement characters
	// over the original.
	ErrNotTextFile = "NOT_TEXT_FILE"
	// ErrUnsupportedFormat is returned (HTTP 415) when a file is a perfectly
	// valid file that this panel has no renderer for — a WebP asked of the
	// thumbnailer, say. Distinct from ErrNotTextFile, which says the file is
	// the wrong kind for the request; this says the request is fine and the
	// panel is the one that falls short.
	ErrUnsupportedFormat = "UNSUPPORTED_FORMAT"
	// ErrStaleWrite is returned (HTTP 409) when a file changed on disk between
	// the read the editor did and the save it is attempting.
	ErrStaleWrite = "STALE_WRITE"
	// ErrDestinationExists is returned (HTTP 409) when a rename, upload or copy
	// would overwrite something and the caller did not ask for that.
	ErrDestinationExists = "DESTINATION_EXISTS"

	// ErrDockerUnavailable is returned (HTTP 503) for a /docker or /compose
	// route on a host where Docker is not reachable. Those routes are not
	// registered at all in that case, so without this the caller got echo's
	// own 404 body, which is not the envelope the client parses — every
	// Docker page then reported "Unknown error" rather than saying Docker
	// was missing.
	ErrDockerUnavailable = "DOCKER_UNAVAILABLE"
)
