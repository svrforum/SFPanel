// Error codes the panel words itself, in the operator's language. The server's
// message is English and stays the fallback; a code missing here shows as sent.
// The error's details (unit, vmid, device…) fill in the message's placeholders.
export const CODE_MESSAGES: Record<string, string> = {
  DOCKER_UNAVAILABLE: 'docker.unavailable',
  DEVICE_IN_USE: 'disk.deviceInUse',
  DISK_NO_PARTITION_TABLE: 'disk.partitions.noTableError',
  DISK_NOT_EMPTY: 'disk.partitions.notEmpty',
  PARTITION_NOT_FORMATTED: 'disk.partitions.notFormatted',
  UFW_LOCKOUT: 'firewall.rules.lockoutBody',
  SERVICE_PROTECTED: 'services.protected',
  SERVICE_CONFIRM_REQUIRED: 'services.confirmRequired',
  PROCESS_PROTECTED: 'processes.protected',
  PROCESS_IS_GUEST: 'processes.isGuest',
  DISK_GUEST_VOLUME: 'disk.guestVolume',
  DISK_BOOT_PARTITION: 'disk.bootPartition',
  LV_HAS_DEPENDENTS: 'disk.lvm.hasDependents',
  LV_SHRINK_CONFIRM_REQUIRED: 'disk.lvm.shrinkConfirmRequired',
  SWAP_UNSUPPORTED_FS: 'disk.swap.unsupportedFs',
  FIREWALL_MANAGED_BY_PVE: 'firewall.managedByPve',
  DOCKER_ON_PVE: 'packages.dockerOnPve',
  NETWORK_READ_ONLY: 'network.readOnly',
  WG_RESOLVCONF_MISSING: 'network.wireguard.resolvconfMissing',
  TAILSCALE_ROUTE_OVERLAP: 'network.tailscale.routeOverlap',
  APT_REPO_UNAUTHORIZED: 'packages.repoUnauthorized',
  APT_REPO_FAILED: 'packages.repoFailed',
  APT_TRANSACTION_RUNNING: 'packages.transactionRunning',
  ALERT_RULE_TYPE_NOT_EVALUATED: 'settings.alerts.rules.typeNotEvaluated',
  CLUSTER_ROUTE_NOT_ON_AGENT: 'cluster.errors.routeNotOnAgent',
  CLUSTER_AGENT_AUTH_FAILED: 'cluster.errors.agentAuthFailed',
  CLUSTER_AGENT_PANEL_INSECURE: 'cluster.errors.agentPanelInsecure',
  CLUSTER_NODE_UNREACHABLE: 'cluster.errors.nodeUnreachable',
  CLUSTER_TOKEN_KIND: 'cluster.errors.tokenKind',
  CLUSTER_MAX_AGENTS: 'cluster.errors.maxAgents',
  CLUSTER_CA_ROTATING: 'cluster.errors.caRotating',
  CLUSTER_QUORUM: 'cluster.errors.quorum',
  UPDATE_IN_PROGRESS: 'settings.updateErrors.inProgress',
  UPDATE_DOWNGRADE_BLOCKED: 'settings.updateErrors.downgrade',
  // Warnings a successful leave, disband or convert carries in data.warnings,
  // worded the same way as a refusal's code.
  CLUSTER_LEAVE_UNACKNOWLEDGED: 'cluster.warnings.leaveUnacknowledged',
  CLUSTER_AGENTS_NOT_REACHED: 'cluster.warnings.agentsNotReached',
  CLUSTER_MANAGER_BACKUPS: 'cluster.warnings.managerBackups',
  RESTORE_MANAGER_BACKUP_ON_AGENT: 'settings.restoreErrors.managerBackupOnAgent',
  RESTORE_AGENT_BACKUP_ON_MANAGER: 'settings.restoreErrors.agentBackupOnManager',
  // CLUSTER_CONVERT_REFUSED, CLUSTER_PROMOTE_REFUSED and CLUSTER_ROTATION_REFUSED
  // carry their reason in the server's message, so they stay unworded here;
  // so does FILE_TOO_LARGE, whose message gives the file's size and the limit.

  // Group tasks: the task and run API on a manager. A value the screen has to
  // put in itself — TASK_TARGET_BUSY's node_id is an ID, not a name, and
  // TASK_CLUSTER_CHANGING's what is an English token — is left out of the
  // sentence; TASK_INVALID's line, on a start or a preview, stays in
  // serverMessage.
  TASK_INVALID: 'cluster.tasks.errors.invalid',
  TASK_TOO_LARGE: 'cluster.tasks.errors.taskTooLarge',
  TASK_NAME_TAKEN: 'cluster.tasks.errors.nameTaken',
  TASK_NOT_FOUND: 'cluster.tasks.errors.notFound',
  TASK_READ_ONLY: 'cluster.tasks.errors.readOnly',
  TASK_RUN_NOT_FOUND: 'cluster.tasks.errors.runNotFound',
  TASK_MANAGERS_NOT_READY: 'cluster.tasks.errors.managersNotReady',
  TASK_INPUT_MISSING: 'cluster.tasks.errors.inputMissing',
  TASK_NO_TARGETS: 'cluster.tasks.errors.noTargets',
  TASK_TOO_MANY_TARGETS: 'cluster.tasks.errors.tooManyTargets',
  TASK_TARGET_BUSY: 'cluster.tasks.errors.targetBusy',
  TASK_PLAN_CHANGED: 'cluster.tasks.errors.planChanged',
  TASK_REBOOT_BUSY: 'cluster.tasks.errors.rebootBusy',
  TASK_CLUSTER_CHANGING: 'cluster.tasks.errors.clusterChanging',
  // Also how a cluster update, a CA rotation, a conversion or a promotion is
  // refused while a run is under way.
  TASK_RUN_ACTIVE: 'cluster.tasks.errors.runActive',
  TASK_JOB_NOT_FOUND: 'cluster.tasks.errors.jobNotFound',
  // A node's half of a run. These reach the page as a preview verdict's code
  // or the "CODE: …" start of a step summary, with no details, so they name
  // no value.
  TASK_JOB_CLUSTER_ONLY: 'cluster.tasks.errors.jobClusterOnly',
  TASK_NODE_BUSY: 'cluster.tasks.errors.nodeBusy',
  TASK_JOB_RUNNING: 'cluster.tasks.errors.jobRunning',
  TASK_REBOOT_PVE_HOST: 'cluster.tasks.errors.rebootPveHost',
  TASK_REBOOT_UNSUPPORTED: 'cluster.tasks.errors.rebootUnsupported',

  // GitHub, for the task import and the compose import alike.
  GIT_AUTH_FAILED: 'common.git.authFailed',
  GIT_REPO_NOT_FOUND: 'common.git.repoNotFound',
  GIT_PATH_NOT_FOUND: 'common.git.pathNotFound',
  GIT_CLONE_FAILED: 'common.git.cloneFailed',
  GIT_RATE_LIMITED: 'common.git.rateLimited',
}

// What a placeholder reads, as an i18n key, when the details carry no value
// for it: a node a server named by neither name nor ID is "that node".
export const CODE_DEFAULTS: Record<string, Record<string, string>> = {
  CLUSTER_NODE_UNREACHABLE: { name: 'cluster.errors.thatNode' },
  // GitHub names when the allowance resets only when it said so.
  GIT_RATE_LIMITED: { reset_at: 'common.git.rateLimitReset' },
  // GitHub's own words for a failure it gave no code to.
  GIT_CLONE_FAILED: { cause: 'common.git.causeUnknown' },
}

// A detail the server sends in a form the operator should not read as sent,
// put in words before it fills the message. A formatter answers null for a
// value it cannot read, and the placeholder then takes its CODE_DEFAULTS
// wording, as if the detail were missing.
export const CODE_DETAIL_FORMATS: Record<string, Record<string, (value: string) => string | null>> = {
  // An RFC 3339 time as this browser's clock reads it. Not "in 12 minutes":
  // the message is worded once, when the error is made, and stays on screen.
  GIT_RATE_LIMITED: { reset_at: localClockTime },
}

const pad2 = (n: number) => String(n).padStart(2, '0')

/** 19:07 for 2026-11-01T10:07:00Z in Seoul; null for anything not an RFC 3339 time. */
export function localClockTime(value: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/.test(value)) return null
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return null
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`
}
