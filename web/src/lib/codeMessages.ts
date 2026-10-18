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
}
