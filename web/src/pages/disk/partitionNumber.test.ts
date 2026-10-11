import { describe, expect, it } from 'vitest'
import { partitionNumber } from './partitionNumber'

describe('partitionNumber', () => {
  it('reads the number after a disk name that ends in a letter', () => {
    expect(partitionNumber('sda', 'sda3')).toBe('3')
    expect(partitionNumber('vdb', 'vdb12')).toBe('12')
  })

  it('skips the "p" the kernel adds after a disk name that ends in a digit', () => {
    expect(partitionNumber('nvme0n1', 'nvme0n1p3')).toBe('3')
    expect(partitionNumber('mmcblk0', 'mmcblk0p1')).toBe('1')
    expect(partitionNumber('loop0', 'loop0p2')).toBe('2')
  })

  it('refuses a name that is not a partition of the disk', () => {
    // sdaa1 is a partition of sdaa, which merely starts like sda.
    expect(partitionNumber('sda', 'sdaa1')).toBeNull()
    expect(partitionNumber('sda', 'sdb1')).toBeNull()
    // Without the "p" this would read as partition 13 of nvme0n1.
    expect(partitionNumber('nvme0n1', 'nvme0n13')).toBeNull()
    expect(partitionNumber('sda', 'sda')).toBeNull()
  })
})
