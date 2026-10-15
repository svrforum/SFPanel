import type { BlockDevice } from '@/types/api'
import { partitionNumber } from './partitionNumber'

// MBR type codes of a DOS extended partition: the container the logical
// partitions (5 and up) live in.
const EXTENDED = new Set(['0x5', '0xf', '0x85'])

// logicalPartitions lists the logical partitions inside p when p is a DOS
// extended partition on disk, or null when p is not one.
export function logicalPartitions(disk: BlockDevice, p: BlockDevice): string[] | null {
  if (disk.pttype !== 'dos' || !EXTENDED.has((p.parttype ?? '').toLowerCase())) return null
  return (disk.children ?? [])
    .filter((c) => Number(partitionNumber(disk.name, c.name)) >= 5)
    .map((c) => c.name)
}

// deleteBlocked says why a partition's Delete is off, or null when it is on.
// The server refuses all three; the page should not offer them.
//   - mounted: deleting it pulls the file system out from under its users;
//   - boot: an EFI System or BIOS boot partition, which Proxmox keeps
//     unmounted, so being unmounted said nothing;
//   - extended: deleting a DOS extended partition deletes the logical ones
//     inside it, such as the LVM physical volume Debian's installer puts
//     the root file system on.
export function deleteBlocked(disk: BlockDevice, p: BlockDevice): 'mounted' | 'boot' | 'extended' | null {
  if (p.mountpoint) return 'mounted'
  if (p.boot) return 'boot'
  if ((logicalPartitions(disk, p) ?? []).length > 0) return 'extended'
  return null
}
