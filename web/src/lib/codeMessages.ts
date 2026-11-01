import type { TFunction } from 'i18next'
import { notReadyText } from './clusterRotation'

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

  // Agent install over SSH: the refusals of /cluster/agent-installs and of the
  // run routes for an install. AGENT_INSTALL_ACTIVE also refuses a task run
  // and a leadership transfer while an install is under way.
  AGENT_INSTALL_INVALID: 'cluster.tasks.install.errors.invalid',
  AGENT_INSTALL_ACTIVE: 'cluster.tasks.install.errors.active',
  AGENT_INSTALL_HOST_KEY_CHANGED: 'cluster.tasks.install.errors.hostKeyChanged',
  AGENT_INSTALL_NOT_WAITING: 'cluster.tasks.install.errors.notWaiting',
  AGENT_INSTALL_UNRELEASED: 'cluster.tasks.install.errors.unreleased',
  AGENT_INSTALL_MIXED_VERSIONS: 'cluster.tasks.install.errors.mixedVersions',
  AGENT_INSTALL_NOT_FOUND: 'cluster.tasks.install.errors.notFound',
  AGENT_INSTALL_NOT_RERUNNABLE: 'cluster.tasks.install.errors.notRerunnable',
  AGENT_INSTALL_LOG_GONE: 'cluster.tasks.install.errors.logGone',
  // An install row's reason, worded with its reason_details by
  // installReasonText (pages/cluster/components/tasks/install/installRun.ts),
  // which picks a closer sentence where the details call for one. Each says
  // what to do. AGENT_INSTALL_HOST_KEY_CHANGED above is a row reason too.
  AGENT_INSTALL_UNREACHABLE: 'cluster.tasks.install.reason.unreachable',
  AGENT_INSTALL_AUTH_FAILED: 'cluster.tasks.install.reason.authFailed',
  AGENT_INSTALL_NOT_ROOT: 'cluster.tasks.install.reason.notRoot',
  AGENT_INSTALL_OS: 'cluster.tasks.install.reason.os',
  AGENT_INSTALL_ARCH: 'cluster.tasks.install.reason.arch',
  AGENT_INSTALL_NO_SYSTEMD: 'cluster.tasks.install.reason.noSystemd',
  AGENT_INSTALL_NO_TOOLS: 'cluster.tasks.install.reason.noTools',
  AGENT_INSTALL_DISK: 'cluster.tasks.install.reason.disk',
  AGENT_INSTALL_PORT_BUSY: 'cluster.tasks.install.reason.portBusy',
  AGENT_INSTALL_CLOCK: 'cluster.tasks.install.reason.clock',
  AGENT_INSTALL_CANNOT_REACH_MANAGER: 'cluster.tasks.install.reason.cannotReachManager',
  AGENT_INSTALL_IP_NOT_LOCAL: 'cluster.tasks.install.reason.ipNotLocal',
  AGENT_INSTALL_NO_INTERNET: 'cluster.tasks.install.reason.noInternet',
  AGENT_INSTALL_ALREADY_AGENT: 'cluster.tasks.install.reason.alreadyAgent',
  AGENT_INSTALL_ALREADY_MANAGER: 'cluster.tasks.install.reason.alreadyManager',
  AGENT_INSTALL_MANAGER_FILES: 'cluster.tasks.install.reason.managerFiles',
  AGENT_INSTALL_OPERATOR_TLS: 'cluster.tasks.install.reason.operatorTls',
  AGENT_INSTALL_NEWER_INSTALLED: 'cluster.tasks.install.reason.newerInstalled',
  AGENT_INSTALL_NAME_INVALID: 'cluster.tasks.install.reason.nameInvalid',
  AGENT_INSTALL_CLUSTER_NOT_READY: 'cluster.tasks.install.reason.clusterNotReady',
  AGENT_INSTALL_IDENTITY_TAKEN: 'cluster.tasks.install.reason.identityTaken',
  AGENT_INSTALL_FAILED: 'cluster.tasks.install.reason.failed',
  AGENT_INSTALL_OFFLINE: 'cluster.tasks.install.reason.offline',
  AGENT_INSTALL_LEADER_CHANGED: 'cluster.tasks.install.reason.leaderChanged',
  AGENT_INSTALL_CREDENTIALS_GONE: 'cluster.tasks.install.reason.credentialsGone',
  // A registered server that was stopped: it stays in the cluster until it
  // is reverted.
  AGENT_INSTALL_STOPPED: 'cluster.tasks.install.reason.stopped',
  // On a succeeded row: the agent is online, without its group label.
  AGENT_INSTALL_LABEL_FAILED: 'cluster.tasks.install.reason.labelFailed',

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
  // An install request's field the screen has no words for (body, accept…).
  AGENT_INSTALL_INVALID: { field: 'cluster.tasks.install.field.form' },
  AGENT_INSTALL_HOST_KEY_CHANGED: { host_key: 'cluster.tasks.install.detail.otherKey' },
  // Row reasons whose detail the server may send empty, or not at all: a
  // server with no os-release, a port owner it could not name ('?'), an
  // SFPanel version it could not read.
  AGENT_INSTALL_UNREACHABLE: { address: 'cluster.tasks.install.detail.theServer' },
  AGENT_INSTALL_NOT_ROOT: { sudo: 'cluster.tasks.install.detail.sudo.unknown' },
  AGENT_INSTALL_OS: { os_id: 'cluster.tasks.install.detail.otherOs' },
  AGENT_INSTALL_ARCH: { arch: 'cluster.tasks.install.detail.unknownCpu' },
  AGENT_INSTALL_PORT_BUSY: { port: 'cluster.tasks.install.detail.agentPort', owner: 'cluster.tasks.install.detail.otherProgram' },
  AGENT_INSTALL_CANNOT_REACH_MANAGER: { manager: 'cluster.tasks.install.detail.theLeader' },
  AGENT_INSTALL_NEWER_INSTALLED: { installed: 'cluster.tasks.install.detail.unreadableVersion' },
  AGENT_INSTALL_NAME_INVALID: { hostname: 'cluster.tasks.install.detail.emptyHostname' },
  AGENT_INSTALL_CLUSTER_NOT_READY: { gate: 'cluster.tasks.install.detail.gate.other' },
  AGENT_INSTALL_FAILED: { step: 'cluster.tasks.install.detail.step.unknown', no_answer: 'cluster.tasks.install.detail.noAnswer.unknown' },
  AGENT_INSTALL_OFFLINE: {
    port: 'cluster.tasks.install.detail.clusterPort',
    ports: 'cluster.tasks.install.detail.agentPorts',
    failed: 'cluster.tasks.install.detail.failed.unknown',
  },
}

// A detail the server sends in a form the operator should not read as sent,
// put in words before it fills the message. A formatter answers null for a
// value it cannot read, and the placeholder then takes its CODE_DEFAULTS
// wording, as if the detail were missing.
export const CODE_DETAIL_FORMATS: Record<string, Record<string, (value: string, t: TFunction) => string | null>> = {
  // An RFC 3339 time as this browser's clock reads it. Not "in 12 minutes":
  // the message is worded once, when the error is made, and stays on screen.
  GIT_RATE_LIMITED: { reset_at: localClockTime },
  // The leader's own lines, "<name> (<reason>)" joined by ", ", each worded
  // as the preview words it. "; " between them: a worded version line holds
  // a comma.
  TASK_MANAGERS_NOT_READY: { managers: (v, t) => v.split(', ').map((line) => notReadyText(line, t)).join('; ') },
  // The request's own name for what is wrong, as the form names it.
  AGENT_INSTALL_INVALID: { field: installFieldText },
  // The install row reasons' tokens, as sentences or words. A token this
  // screen does not know takes the CODE_DEFAULTS wording.
  AGENT_INSTALL_NOT_ROOT: { sudo: sudoText },
  AGENT_INSTALL_NO_TOOLS: { missing: listText },
  AGENT_INSTALL_MANAGER_FILES: { files: listText },
  AGENT_INSTALL_DISK: { free_kb: kibText, need_kb: kibText },
  AGENT_INSTALL_PORT_BUSY: { owner: (v) => (v === '?' ? null : v) },
  // Behind or ahead: the operator fixes either the same way.
  AGENT_INSTALL_CLOCK: { skew_seconds: (v) => (/^-?\d+$/.test(v) ? String(Math.abs(Number(v))) : null) },
  AGENT_INSTALL_CLUSTER_NOT_READY: { gate: gateText },
  AGENT_INSTALL_IDENTITY_TAKEN: { row: rowText },
  AGENT_INSTALL_FAILED: { step: stepText, no_answer: noAnswerText },
  AGENT_INSTALL_OFFLINE: { ports: listText, failed: failedCallsText },
}

const pad2 = (n: number) => String(n).padStart(2, '0')

/** 19:07 for 2026-11-01T10:07:00Z in Seoul; null for anything not an RFC 3339 time. */
export function localClockTime(value: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/.test(value)) return null
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return null
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`
}

// The words of the agent install's detail tokens. Every key is written out
// whole, so locales.test checks it in both languages.

function installFieldText(v: string, t: TFunction): string | null {
  switch (v) {
    case 'servers': return t('cluster.tasks.install.field.servers')
    case 'host': return t('cluster.tasks.install.field.host')
    case 'port': return t('cluster.tasks.install.field.port')
    case 'name': return t('cluster.tasks.install.field.name')
    case 'group': return t('cluster.tasks.install.field.group')
    case 'user': return t('cluster.tasks.install.field.user')
    case 'method': return t('cluster.tasks.install.field.method')
    case 'password': return t('cluster.tasks.install.field.password')
    case 'key': return t('cluster.tasks.install.field.key')
    case 'passphrase': return t('cluster.tasks.install.field.passphrase')
    case 'sudo_same':
    case 'sudo_password': return t('cluster.tasks.install.field.sudoPassword')
    case 'mode': return t('cluster.tasks.install.field.mode')
    case 'concurrency': return t('cluster.tasks.install.field.concurrency')
    default: return null
  }
}

function sudoText(v: string, t: TFunction): string | null {
  switch (v) {
    case 'none': return t('cluster.tasks.install.detail.sudo.none')
    case 'password': return t('cluster.tasks.install.detail.sudo.password')
    case 'refused': return t('cluster.tasks.install.detail.sudo.refused')
    default: return null
  }
}

function gateText(v: string, t: TFunction): string | null {
  switch (v) {
    case 'proxy_rotation': return t('cluster.tasks.install.detail.gate.proxyRotation')
    case 'ca_rotation': return t('cluster.tasks.install.detail.gate.caRotation')
    case 'managers': return t('cluster.tasks.install.detail.gate.managers')
    case 'max_agents': return t('cluster.tasks.install.detail.gate.maxAgents')
    case 'ca_key': return t('cluster.tasks.install.detail.gate.caKey')
    default: return null
  }
}

function stepText(v: string, t: TFunction): string | null {
  switch (v) {
    case 'ssh': return t('cluster.tasks.install.detail.step.ssh')
    case 'login_check': return t('cluster.tasks.install.detail.step.loginCheck')
    case 'probe': return t('cluster.tasks.install.detail.step.probe')
    case 'checksums': return t('cluster.tasks.install.detail.step.checksums')
    case 'archive': return t('cluster.tasks.install.detail.step.archive')
    case 'ca_pin': return t('cluster.tasks.install.detail.step.caPin')
    case 'mktemp': return t('cluster.tasks.install.detail.step.mktemp')
    case 'systemd-run': return t('cluster.tasks.install.detail.step.systemdRun')
    case 'install': return t('cluster.tasks.install.detail.step.install')
    case 'register': return t('cluster.tasks.install.detail.step.register')
    case 'labels': return t('cluster.tasks.install.detail.step.labels')
    default: return null
  }
}

function noAnswerText(v: string, t: TFunction): string | null {
  switch (v) {
    case 'host_key': return t('cluster.tasks.install.detail.noAnswer.hostKey')
    case 'standalone': return t('cluster.tasks.install.detail.noAnswer.standalone')
    case 'review': return t('cluster.tasks.install.detail.noAnswer.review')
    default: return null
  }
}

function failedCallsText(v: string, t: TFunction): string | null {
  switch (v) {
    case 'relay': return t('cluster.tasks.install.detail.failed.relay')
    case 'panel': return t('cluster.tasks.install.detail.failed.panel')
    case 'relay,panel': return t('cluster.tasks.install.detail.failed.both')
    default: return null
  }
}

// s2 → server 2: the row in the order the servers were given.
function rowText(v: string, t: TFunction): string | null {
  const m = /^s(\d+)$/.exec(v)
  return m ? t('cluster.tasks.install.detail.row', { n: m[1] }) : null
}

// bash,curl → bash, curl.
function listText(v: string): string | null {
  const items = v.split(',').map((s) => s.trim()).filter(Boolean)
  return items.length ? items.join(', ') : null
}

// 20480 → 20 MiB. A size under 1 MiB still reads as 1 MiB, never 0.
function kibText(v: string): string | null {
  if (!/^\d+$/.test(v)) return null
  return `${Math.max(1, Math.round(Number(v) / 1024))} MiB`
}
