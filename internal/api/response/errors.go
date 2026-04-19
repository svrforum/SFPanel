package response

// Common error codes
const (
	ErrInvalidRequest  = "INVALID_REQUEST"
	ErrInvalidBody     = "INVALID_BODY"
	ErrMissingFields   = "MISSING_FIELDS"
	ErrNotFound        = "NOT_FOUND"
	ErrAlreadyExists   = "ALREADY_EXISTS"
	ErrInternalError   = "INTERNAL_ERROR"
	ErrDBError         = "DB_ERROR"
	ErrReadError       = "READ_ERROR"
	ErrWriteError      = "WRITE_ERROR"
	ErrDeleteError     = "DELETE_ERROR"
	ErrDirError        = "DIR_ERROR"
	ErrIOError         = "IO_ERROR"
	ErrEmptyContent    = "EMPTY_CONTENT"
	ErrInvalidValue    = "INVALID_VALUE"
	ErrInvalidName     = "INVALID_NAME"
	ErrInvalidID       = "INVALID_ID"
	ErrInvalidAction   = "INVALID_ACTION"
	ErrInvalidPath     = "INVALID_PATH"
	ErrMissingPath     = "MISSING_PATH"
	ErrPermissionDenied = "PERMISSION_DENIED"
	ErrSSEError        = "SSE_ERROR"
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
)

// Docker error codes
const (
	ErrDockerError         = "DOCKER_ERROR"
	ErrDockerFirewallError = "DOCKER_FIREWALL_ERROR"
	ErrComposeError        = "COMPOSE_ERROR"
)

// File error codes
const (
	ErrFileError      = "FILE_ERROR"
	ErrFileNotFound   = "FILE_NOT_FOUND"
	ErrFileDeleteError = "FILE_DELETE_ERROR"
	ErrFileWriteError = "FILE_WRITE_ERROR"
	ErrFileTooLarge   = "FILE_TOO_LARGE"
	ErrMissingFile    = "MISSING_FILE"
	ErrNotAFile       = "NOT_A_FILE"
	ErrIsDirectory    = "IS_DIRECTORY"
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
)

// Package (APT) error codes
const (
	ErrAPTError          = "APT_ERROR"
	ErrAPTUpdateError    = "APT_UPDATE_ERROR"
	ErrAPTInstallError   = "APT_INSTALL_ERROR"
	ErrAPTRemoveError    = "APT_REMOVE_ERROR"
	ErrAPTSearchError    = "APT_SEARCH_ERROR"
	ErrAPTUpgradeError   = "APT_UPGRADE_ERROR"
	ErrInstallError      = "INSTALL_ERROR"
	ErrInvalidPackageName = "INVALID_PACKAGE_NAME"
)

// Firewall error codes
const (
	ErrFirewallError    = "FIREWALL_ERROR"
	ErrUFWError         = "UFW_ERROR"
	ErrUFWEnableError   = "UFW_ENABLE_ERROR"
	ErrUFWDisableError  = "UFW_DISABLE_ERROR"
	ErrUFWAddRuleError  = "UFW_ADD_RULE_ERROR"
	ErrUFWDeleteError   = "UFW_DELETE_ERROR"
	ErrIPTablesError    = "IPTABLES_ERROR"
	ErrFail2banError    = "FAIL2BAN_ERROR"
	ErrInvalidPort      = "INVALID_PORT"
	ErrInvalidProtocol  = "INVALID_PROTOCOL"
	ErrInvalidIP        = "INVALID_IP"
	ErrInvalidRuleNumber = "INVALID_RULE_NUMBER"
	ErrInvalidFromAddress = "INVALID_FROM_ADDRESS"
	ErrInvalidToAddress = "INVALID_TO_ADDRESS"
	ErrInvalidJailName  = "INVALID_JAIL_NAME"
	ErrMissingJailName  = "MISSING_JAIL_NAME"
	ErrJailExists       = "JAIL_EXISTS"
	ErrInvalidBanTime   = "INVALID_BAN_TIME"
	ErrInvalidFindTime  = "INVALID_FIND_TIME"
	ErrInvalidMaxRetry  = "INVALID_MAX_RETRY"
	ErrInvalidLogPath   = "INVALID_LOG_PATH"
	ErrInvalidFilterName = "INVALID_FILTER_NAME"
	ErrUnknownTemplate  = "UNKNOWN_TEMPLATE"
	ErrEnableFailed     = "ENABLE_FAILED"
	ErrDisableFailed    = "DISABLE_FAILED"
	ErrStartFailed      = "START_FAILED"
	ErrStopFailed       = "STOP_FAILED"
	ErrRestartFailed    = "RESTART_FAILED"
)

// Disk error codes
const (
	ErrDiskError        = "DISK_ERROR"
	ErrLVMError         = "LVM_ERROR"
	ErrRAIDError        = "RAID_ERROR"
	ErrSwapError        = "SWAP_ERROR"
	ErrSMARTError       = "SMART_ERROR"
	ErrPartitionError   = "PARTITION_ERROR"
	ErrFSError          = "FS_ERROR"
	ErrFormatError      = "FORMAT_ERROR"
	ErrMountError       = "MOUNT_ERROR"
	ErrUnmountError     = "UNMOUNT_ERROR"
	ErrResizeError      = "RESIZE_ERROR"
	ErrExpandError      = "EXPAND_ERROR"
	ErrInvalidDevice    = "INVALID_DEVICE"
	ErrInvalidPartition = "INVALID_PARTITION"
	ErrInvalidFSType    = "INVALID_FSTYPE"
	ErrInvalidMountpoint = "INVALID_MOUNTPOINT"
	ErrInvalidOptions   = "INVALID_OPTIONS"
	ErrInvalidSize      = "INVALID_SIZE"
	ErrInvalidStart     = "INVALID_START"
	ErrInvalidEnd       = "INVALID_END"
	ErrInvalidVG        = "INVALID_VG"
	ErrInvalidLevel     = "INVALID_LEVEL"
)

// Network error codes
const (
	ErrNetworkError   = "NETWORK_ERROR"
	ErrNetplanError   = "NETPLAN_ERROR"
	ErrSSError        = "SS_ERROR"
)

// WireGuard error codes
const (
	ErrWireGuardError   = "WIREGUARD_ERROR"
	ErrWGListError      = "WG_LIST_ERROR"
	ErrWGInstallError   = "WG_INSTALL_ERROR"
	ErrWGUpError        = "WG_UP_ERROR"
	ErrWGDownError      = "WG_DOWN_ERROR"
)

// Tailscale error codes
const (
	ErrTailscaleError = "TAILSCALE_ERROR"
	ErrTSUpError      = "TS_UP_ERROR"
	ErrTSDownError    = "TS_DOWN_ERROR"
	ErrTSLogoutError  = "TS_LOGOUT_ERROR"
	ErrTSPeersError   = "TS_PEERS_ERROR"
	ErrTSSetError     = "TS_SET_ERROR"
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
	ErrAppStoreError       = "APPSTORE_ERROR"
	ErrPortConflict        = "PORT_CONFLICT"
	ErrContainerConflict   = "CONTAINER_CONFLICT"
)

// Alert error codes
const (
	ErrAlertError   = "ALERT_ERROR"
	ErrChannelError = "CHANNEL_ERROR"
	ErrRuleError    = "RULE_ERROR"
)

// System update error codes
const (
	ErrUpdateCheckFailed = "UPDATE_CHECK_FAILED"
	ErrUpdateFailed      = "UPDATE_FAILED"
	ErrBackupFailed      = "BACKUP_FAILED"
	ErrRestoreFailed     = "RESTORE_FAILED"
)

// Command execution error codes
const (
	ErrCommandTimeout = "COMMAND_TIMEOUT"
)
