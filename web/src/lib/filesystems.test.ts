import { describe, it, expect } from 'vitest'
import { isRealFilesystem, worstFilesystem, rootFilesystem } from './filesystems'
import type { Filesystem } from '@/types/api'

const fs = (o: Partial<Filesystem>): Filesystem => ({
  source: '/dev/sda1',
  fstype: 'ext4',
  size: 100,
  used: 50,
  available: 50,
  use_percent: 50,
  mount_point: '/',
  ...o,
})

describe('isRealFilesystem', () => {
  it('accepts block devices and mounted network shares', () => {
    expect(isRealFilesystem(fs({ source: '/dev/sda2' }))).toBe(true)
    expect(isRealFilesystem(fs({ source: 'nas.example:/vol', fstype: 'nfs4' }))).toBe(true)
    expect(isRealFilesystem(fs({ source: '//nas/share', fstype: 'cifs' }))).toBe(true)
  })

  it('rejects the pseudo filesystems df reports alongside them', () => {
    // Each of these is routinely at or near 100% on a healthy host, which is
    // exactly why the card must not lead with them.
    expect(isRealFilesystem(fs({ source: 'overlay', fstype: 'overlay', use_percent: 100 }))).toBe(false)
    expect(isRealFilesystem(fs({ source: 'tmpfs', fstype: 'tmpfs', use_percent: 99 }))).toBe(false)
    expect(isRealFilesystem(fs({ source: '/var/lib/snapd/snaps/core.snap', fstype: 'squashfs', use_percent: 100 }))).toBe(false)
    expect(isRealFilesystem(fs({ source: 'efivarfs', fstype: 'efivarfs', use_percent: 59 }))).toBe(false)
  })

  it('rejects a zero-sized mount rather than dividing by it', () => {
    expect(isRealFilesystem(fs({ size: 0 }))).toBe(false)
  })
})

describe('worstFilesystem', () => {
  it('picks the fullest real filesystem, not the fullest line of df', () => {
    const list = [
      fs({ mount_point: '/', use_percent: 46 }),
      fs({ source: 'overlay', fstype: 'overlay', mount_point: '/var/lib/docker/o', use_percent: 100 }),
      fs({ source: 'nas.example:/vol', fstype: 'nfs4', mount_point: '/mnt/nas', use_percent: 91 }),
      fs({ source: '/dev/sda1', mount_point: '/boot/efi', use_percent: 1 }),
    ]
    const worst = worstFilesystem(list)
    expect(worst?.mount_point).toBe('/mnt/nas')
    expect(worst?.use_percent).toBe(91)
  })

  it('returns null when df gave nothing usable', () => {
    expect(worstFilesystem([])).toBeNull()
    expect(worstFilesystem(null)).toBeNull()
    expect(worstFilesystem([fs({ source: 'tmpfs', fstype: 'tmpfs' })])).toBeNull()
  })
})

describe('rootFilesystem', () => {
  it('finds / and ignores a pseudo mount that claims it', () => {
    const list = [fs({ mount_point: '/mnt/nas', source: '/dev/sdb1' }), fs({ mount_point: '/' })]
    expect(rootFilesystem(list)?.mount_point).toBe('/')
    expect(rootFilesystem([fs({ mount_point: '/', source: 'rootfs', fstype: 'rootfs' })])).toBeNull()
  })
})
