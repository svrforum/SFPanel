import { describe, expect, it } from 'vitest'
import type { BlockDevice } from '@/types/api'
import { diskOf, freeDevices } from './freeDevices'

const dev = (name: string, extra: Partial<BlockDevice> = {}): BlockDevice => ({
  name, size: 1, type: 'part', fstype: '', mountpoint: '', model: '', serial: '',
  rotational: false, readonly: false, transport: '', state: '', vendor: '', ...extra,
})

// A Proxmox VE host: the boot disk with its two boot partitions and a ZFS
// member, a data disk with an extended partition, a disk nothing is on, a
// root disk whose LVM partition holds volumes, and two guests' zvols.
const disks: BlockDevice[] = [
  dev('zd0', { type: 'disk', fstype: 'swap', mountpoint: '[SWAP]', owner: 'zvol rpool/swap' }),
  dev('nvme0n1', {
    type: 'disk', model: 'Example NVMe', pttype: 'gpt', children: [
      dev('nvme0n1p1', { parttypename: 'BIOS boot', boot: true }),
      dev('nvme0n1p2', { fstype: 'vfat', parttypename: 'EFI System', boot: true }),
      dev('nvme0n1p3', { fstype: 'zfs_member' }),
    ],
  }),
  dev('sdb', {
    type: 'disk', model: 'Example HDD', pttype: 'dos', children: [
      dev('sdb1', { fstype: 'ext4', parttype: '0x83' }),
      dev('sdb2', { parttype: '0x5', parttypename: 'Extended' }),
      dev('sdb5', { fstype: 'ext4', parttype: '0x83', mountpoint: '/srv' }),
      dev('sdb6', { fstype: 'swap', parttype: '0x82', mountpoint: '[SWAP]' }),
    ],
  }),
  dev('sdc', { type: 'disk', model: 'Blank SSD' }),
  dev('sda', {
    type: 'disk', children: [
      dev('sda1', { fstype: 'LVM2_member', children: [dev('pve-root', { type: 'lvm', mountpoint: '/' })] }),
    ],
  }),
  dev('zd16', { type: 'disk', owner: 'VMID 100', children: [dev('zd16p1', { fstype: 'ext4' })] }),
]

describe('freeDevices', () => {
  it('offers a blank disk and the partitions nothing holds', () => {
    expect(freeDevices(disks).map((d) => d.name)).toEqual(['nvme0n1p3', 'sdb1', 'sdc'])
  })

  it('keeps out a guest volume and its partitions, boot partitions, mounted and swap ones, and what holds volumes', () => {
    const names = freeDevices(disks).map((d) => d.name)
    for (const held of ['zd0', 'zd16', 'zd16p1', 'nvme0n1p1', 'nvme0n1p2', 'sdb5', 'sdb6', 'sda1', 'nvme0n1', 'sdb', 'sda']) {
      expect(names).not.toContain(held)
    }
  })

  // Writing over an extended partition writes over the table of the logical
  // partitions inside it.
  it('keeps out a DOS extended partition', () => {
    expect(freeDevices(disks).map((d) => d.name)).not.toContain('sdb2')
  })

  it('takes an answer with no disks', () => {
    expect(freeDevices([])).toEqual([])
  })
})

describe('diskOf', () => {
  it('finds the disk a partition is on, and a disk is its own', () => {
    expect(diskOf(disks, 'sdb1')?.name).toBe('sdb')
    expect(diskOf(disks, 'sdc')?.name).toBe('sdc')
    expect(diskOf(disks, 'nope')).toBeUndefined()
  })
})
