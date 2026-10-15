import type { LogicalVolume } from '@/types/api'

// lv_attr's first letter is the volume type: t for a thin pool.
export function isThinPool(lv: LogicalVolume): boolean {
  return lv.attr.startsWith('t')
}

export interface LVRow {
  lv: LogicalVolume
  // A thin volume shown under the pool it lives in.
  nested: boolean
}

// lvRows orders the list the way the volumes stack: each thin pool followed
// by the thin volumes in it, everything else in the order LVM gave. A flat
// list put a Proxmox host's pool among dozens of guest disks with nothing to
// say which were in it.
export function lvRows(lvs: LogicalVolume[]): LVRow[] {
  const key = (vg: string, name: string) => `${vg}/${name}`
  const pools = new Set(lvs.filter(isThinPool).map((lv) => key(lv.vg_name, lv.name)))
  const rows: LVRow[] = []
  for (const lv of lvs) {
    if (lv.pool_lv && pools.has(key(lv.vg_name, lv.pool_lv))) continue
    rows.push({ lv, nested: false })
    if (isThinPool(lv)) {
      for (const v of lvs) {
        if (v.vg_name === lv.vg_name && v.pool_lv === lv.name) rows.push({ lv: v, nested: true })
      }
    }
  }
  return rows
}

// fillPercent reads lvs' "81.20" as a number, or null when LVM gave none.
export function fillPercent(value: string | undefined): number | null {
  if (!value) return null
  const n = Number(value)
  return Number.isFinite(n) ? n : null
}
