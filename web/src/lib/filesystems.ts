import type { Filesystem } from '@/types/api'

/** Mirrors isNetworkFstype in internal/feature/disk/netshare.go. */
const NETWORK_FSTYPES = new Set(['cifs', 'smbfs', 'nfs', 'nfs4'])

/**
 * True for a filesystem an operator can actually fill up.
 *
 * df reports overlay layers, squashfs snaps, tmpfs and every container mount
 * alongside the real ones. A dashboard that took the fullest of *those* would
 * cry wolf constantly: a 100%-full squashfs is what a squashfs looks like, and
 * tmpfs sizing tells you about RAM, not storage.
 *
 * The rule matches the backend's own ranking (sortFilesystems in
 * internal/feature/disk/disk_filesystems.go): a block device, or a mounted
 * network share — which includes the SMB and NFS drives this panel attaches
 * itself.
 */
export function isRealFilesystem(fs: Filesystem): boolean {
  // An unresponsive share has no numbers to rank by; it is the disk page's
  // job to show that it is down, not the dashboard's to call it 0% full.
  if (!fs || fs.size <= 0 || fs.unresponsive) return false
  if (NETWORK_FSTYPES.has(fs.fstype)) return true
  return fs.source.startsWith('/dev/')
}

/**
 * The filesystem closest to full, or null when none is worth reporting.
 *
 * The dashboard's disk card reads disk.Usage("/") on the server, so a host with
 * a media array on /mnt or a share this panel mounted can sit at 99% while the
 * card shows a calm 34%. This is what the card should lead with.
 */
export function worstFilesystem(list: Filesystem[] | null | undefined): Filesystem | null {
  if (!list || list.length === 0) return null
  let worst: Filesystem | null = null
  for (const fs of list) {
    if (!isRealFilesystem(fs)) continue
    if (!worst || fs.use_percent > worst.use_percent) worst = fs
  }
  return worst
}

/** The root filesystem, for the card's secondary line. */
export function rootFilesystem(list: Filesystem[] | null | undefined): Filesystem | null {
  if (!list) return null
  return list.find((fs) => fs.mount_point === '/' && isRealFilesystem(fs)) ?? null
}
