import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import { formatBytes } from '@/lib/utils'
import { Input } from '@/components/ui/input'
import { NativeSelect } from './NativeSelect'
import { diskOf } from '../freeDevices'
import type { BlockDevice } from '@/types/api'

/**
 * A device picked from the ones nothing holds (see freeDevices), named with
 * its size, what is on it and the disk's model, in place of a typed name.
 */
// The select's own value for "a device that is not listed".
const OTHER = '\u0000other'

export function FreeDeviceSelect({ id, value, onChange, disks, free, loading, failed, invalid, describedBy, allowOther }: {
  id: string
  value: string
  onChange: (name: string) => void
  /** The dialog's action was pressed with nothing picked. */
  invalid?: boolean
  describedBy?: string
  /**
   * Also let a name be typed. A logical volume or a crypt mapping takes a file
   * system too, and what lsblk calls one ("vg0-data") is not its name under
   * /dev (dm-0), so it cannot be listed; without this the only way left was a
   * terminal. Not for the members of a new array.
   */
  allowOther?: boolean
  disks: BlockDevice[]
  free: BlockDevice[]
  loading: boolean
  failed: boolean
}) {
  const { t } = useTranslation()
  const [other, setOther] = useState(false)
  if (loading) {
    return (
      <p className="flex items-center gap-2 py-2 text-[13px] text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
        {t('common.loading')}
      </p>
    )
  }
  if (failed) return <p role="alert" className="text-[13px] text-destructive">{t('disk.overview.fetchFailed')}</p>
  if (free.length === 0 && !allowOther) return <p className="text-[13px] text-muted-foreground">{t('disk.noFreeDevices')}</p>
  // With nothing to list, typing is all there is.
  const typing = other || free.length === 0
  return (
    <div className="space-y-2">
      {free.length === 0 ? (
        <p className="text-[13px] text-muted-foreground">{t('disk.noFreeDevices')}</p>
      ) : (
        <NativeSelect
          id={id}
          value={typing ? OTHER : value}
          onChange={(e) => {
            const picked = e.target.value
            setOther(picked === OTHER)
            onChange(picked === OTHER ? '' : picked)
          }}
          aria-invalid={(invalid && !typing) || undefined}
          aria-describedby={describedBy}
          className="w-full"
        >
          <option value="" disabled>{t('disk.pickDevice')}</option>
          {free.map((d) => {
            const model = diskOf(disks, d.name)?.model
            return (
              <option key={d.name} value={d.name}>
                {[`${d.name} — ${formatBytes(d.size)}`, d.fstype, model].filter(Boolean).join(' · ')}
              </option>
            )
          })}
          {allowOther && <option value={OTHER}>{t('disk.otherDevice')}</option>}
        </NativeSelect>
      )}
      {typing && (
        <>
          <Input
            id={free.length === 0 ? id : `${id}-other`}
            aria-label={t('disk.otherDeviceLabel')}
            aria-invalid={invalid || undefined}
            aria-describedby={`${id}-other-hint`}
            value={value}
            onChange={(e) => onChange(e.target.value.trim())}
            placeholder="dm-0"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            className="font-mono text-base md:text-[13px]"
          />
          <p id={`${id}-other-hint`} className="text-[12px] text-muted-foreground">{t('disk.otherDeviceHint')}</p>
        </>
      )}
    </div>
  )
}

/** What is on a device, shown where the operator confirms writing over it. */
export function DeviceFacts({ device, disks }: { device: BlockDevice | undefined; disks: BlockDevice[] }) {
  const { t } = useTranslation()
  if (!device) return null
  const disk = diskOf(disks, device.name)
  const partOf = disk && disk.name !== device.name ? disk : undefined
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 rounded-xl bg-muted/30 p-3 text-[13px]">
      <dt className="text-muted-foreground">{t('disk.overview.size')}</dt>
      <dd className="font-mono">{formatBytes(device.size)}</dd>
      <dt className="text-muted-foreground">{t('disk.facts.fstype')}</dt>
      <dd className="font-mono">{device.fstype || t('disk.facts.none')}</dd>
      {device.label && (
        <>
          <dt className="text-muted-foreground">{t('disk.facts.label')}</dt>
          <dd className="break-words">{device.label}</dd>
        </>
      )}
      {device.parttypename && (
        <>
          <dt className="text-muted-foreground">{t('disk.partitions.type')}</dt>
          <dd>{device.parttypename}</dd>
        </>
      )}
      {partOf && (
        <>
          <dt className="text-muted-foreground">{t('disk.facts.disk')}</dt>
          <dd className="font-mono break-all">{partOf.name}</dd>
        </>
      )}
      <dt className="text-muted-foreground">{t('disk.overview.model')}</dt>
      <dd className="break-words">{disk?.model || t('disk.overview.unknownModel')}</dd>
    </dl>
  )
}
