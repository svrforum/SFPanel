import { describe, expect, it } from 'vitest'
import type { LogicalVolume } from '@/types/api'
import { fillPercent, lvRows } from './lvRows'

const lv = (name: string, attr: string, pool = '', vg = 'pve'): LogicalVolume => ({
  name, vg_name: vg, size: '1g', attr, path: '', pool_lv: pool, data_percent: '',
})

describe('lvRows', () => {
  it('puts each thin volume under its pool', () => {
    const rows = lvRows([
      lv('vm-100-disk-0', 'Vwi-aotz--', 'data'),
      lv('root', '-wi-ao----'),
      lv('data', 'twi-aotz--'),
      lv('vm-101-disk-0', 'Vwi-a-tz--', 'data'),
      lv('swap', '-wi-ao----'),
    ])
    expect(rows.map((r) => `${r.nested ? '  ' : ''}${r.lv.name}`)).toEqual([
      'root', 'data', '  vm-100-disk-0', '  vm-101-disk-0', 'swap',
    ])
  })

  it('keeps pools of the same name in different VGs apart, and a volume whose pool is not listed', () => {
    const rows = lvRows([
      lv('data', 'twi-aotz--', '', 'vg1'),
      lv('a', 'Vwi-a-tz--', 'data', 'vg2'),
      lv('data', 'twi-aotz--', '', 'vg2'),
      lv('orphan', 'Vwi-a-tz--', 'gone', 'vg1'),
    ])
    expect(rows.map((r) => `${r.nested ? '  ' : ''}${r.lv.vg_name}/${r.lv.name}`)).toEqual([
      'vg1/data', 'vg2/data', '  vg2/a', 'vg1/orphan',
    ])
  })
})

describe('fillPercent', () => {
  it('reads what lvs prints', () => {
    expect(fillPercent('81.20')).toBe(81.2)
    expect(fillPercent('')).toBeNull()
    expect(fillPercent(undefined)).toBeNull()
  })
})
