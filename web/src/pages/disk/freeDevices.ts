import type { BlockDevice } from '@/types/api'
import { logicalPartitions } from './partitionRules'

// freeDevices lists what can be taken whole for a new use — an array member,
// a file system, a physical volume, swap: a disk with no partitions, and the
// partitions, that nothing holds. Picking from it replaces typing a device
// name, where a typo reached an in-use or data disk and the confirmation only
// asked for the same typo again.
//   - a guest's volume on Proxmox VE (owner set), and the guest's own
//     partitions inside it, are not the host's;
//   - a mount point (a swap partition's is "[SWAP]") means it is in use;
//   - children — LVM volumes, an md array, a crypt mapping — mean it holds
//     something;
//   - a boot partition is never mounted on Proxmox VE and is still not free;
//   - a DOS extended partition is the table of the logical ones inside it.
export function freeDevices(disks: BlockDevice[]): BlockDevice[] {
  const free: BlockDevice[] = []
  for (const d of disks) {
    if (d.owner) continue
    const parts = d.children ?? []
    if ((d.type === 'disk' || !d.type) && parts.length === 0 && !d.mountpoint) free.push(d)
    for (const c of parts) {
      if (c.type !== 'part' || c.mountpoint || c.boot || (c.children?.length ?? 0) > 0) continue
      if (logicalPartitions(d, c) !== null) continue
      free.push(c)
    }
  }
  return free
}

// diskOf finds the disk a listed device is on; a whole disk is its own.
export function diskOf(disks: BlockDevice[], name: string): BlockDevice | undefined {
  return disks.find((d) => d.name === name || (d.children ?? []).some((c) => c.name === name))
}
