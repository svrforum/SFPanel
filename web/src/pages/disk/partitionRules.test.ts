import { describe, expect, it } from 'vitest'
import type { BlockDevice } from '@/types/api'
import { deleteBlocked, logicalPartitions } from './partitionRules'

const dev = (name: string, extra: Partial<BlockDevice> = {}): BlockDevice => ({
  name, size: 1, type: 'part', fstype: '', mountpoint: '', model: '', serial: '',
  rotational: false, readonly: false, transport: '', state: '', vendor: '', ...extra,
})

// Debian's guided LVM on a BIOS machine: /boot, an extended partition, and
// inside it the physical volume under root.
const debian = dev('sda', {
  type: 'disk', pttype: 'dos', children: [
    dev('sda1', { parttype: '0x83', mountpoint: '/boot' }),
    dev('sda2', { parttype: '0x5', parttypename: 'Extended' }),
    dev('sda5', { parttype: '0x8e', fstype: 'LVM2_member' }),
  ],
})

describe('partition rules', () => {
  it('knows a DOS extended partition and what is inside it', () => {
    const [, extended, logical] = debian.children!
    expect(logicalPartitions(debian, extended)).toEqual(['sda5'])
    expect(logicalPartitions(debian, logical)).toBeNull()
    expect(deleteBlocked(debian, extended)).toBe('extended')
    expect(deleteBlocked(debian, logical)).toBeNull()
    expect(deleteBlocked(debian, debian.children![0])).toBe('mounted')
  })

  it('lets an empty extended partition go', () => {
    const disk = dev('sdb', { type: 'disk', pttype: 'dos', children: [dev('sdb1', { parttype: '0xf' })] })
    expect(logicalPartitions(disk, disk.children![0])).toEqual([])
    expect(deleteBlocked(disk, disk.children![0])).toBeNull()
  })

  it('keeps the Delete of a boot partition off though nothing mounts it', () => {
    const disk = dev('nvme0n1', {
      type: 'disk', pttype: 'gpt', children: [dev('nvme0n1p2', { parttype: 'c12a7328-f81f-11d2-ba4b-00a0c93ec93b', fstype: 'vfat', boot: true })],
    })
    expect(deleteBlocked(disk, disk.children![0])).toBe('boot')
  })

  it('reads 0x5 as a type code only on a DOS table', () => {
    const disk = dev('sdc', { type: 'disk', pttype: 'gpt', children: [dev('sdc2', { parttype: '0x5' }), dev('sdc5')] })
    expect(logicalPartitions(disk, disk.children![0])).toBeNull()
  })
})
