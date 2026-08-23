import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import type { FileEntry } from '@/types/api'
import { modeStringToOctal } from '../modeString'

type Who = 'user' | 'group' | 'other'
type What = 'r' | 'w' | 'x'

const WHO: Who[] = ['user', 'group', 'other']
const WHAT: What[] = ['r', 'w', 'x']
const BIT: Record<What, number> = { r: 4, w: 2, x: 1 }

/**
 * Edit permissions and ownership.
 *
 * Neither was reachable before. The listing showed drwxr-xr-x — what the owner
 * may do — without ever saying who the owner is, and offered no way to change
 * either. The most common problem on a homelab box is a container writing files
 * as a uid the operator's account cannot touch; the panel could show the
 * symptom and do nothing about it.
 */
export function PermissionsDialog({ entry, onOpenChange, onApplied }: {
  entry: FileEntry | null
  onOpenChange: (open: boolean) => void
  onApplied: () => void
}) {
  const { t } = useTranslation()
  const [octal, setOctal] = useState('644')
  const [owner, setOwner] = useState('')
  const [group, setGroup] = useState('')
  const [recursive, setRecursive] = useState(false)
  const [saving, setSaving] = useState(false)

  // Seed from the entry before paint, so the dialog opens showing what the
  // file actually is rather than a default that would silently change it.
  const [prevEntry, setPrevEntry] = useState<FileEntry | null>(entry)
  if (prevEntry !== entry) {
    setPrevEntry(entry)
    if (entry) {
      setOctal(modeStringToOctal(entry.mode))
      setOwner(entry.owner?.user || (entry.owner ? String(entry.owner.uid) : ''))
      setGroup(entry.owner?.group || (entry.owner ? String(entry.owner.gid) : ''))
      setRecursive(false)
    }
  }

  const digits = useMemo(() => octal.padStart(3, '0').slice(-3).split('').map(Number), [octal])

  const toggle = (whoIndex: number, what: What) => {
    const next = [...digits]
    next[whoIndex] = next[whoIndex] ^ BIT[what]
    setOctal(next.join(''))
  }

  const originalOctal = entry ? modeStringToOctal(entry.mode) : ''
  const originalOwner = entry?.owner?.user || (entry?.owner ? String(entry.owner.uid) : '')
  const originalGroup = entry?.owner?.group || (entry?.owner ? String(entry.owner.gid) : '')
  const modeChanged = octal !== originalOctal
  const ownerChanged = owner !== originalOwner || group !== originalGroup

  const handleApply = async () => {
    if (!entry) return
    setSaving(true)
    try {
      // Only send what actually changed. Issuing a chown that sets the same
      // owner is a needless write, and on a large tree a needless recursive
      // walk.
      if (modeChanged) await api.chmodPath(entry.path, octal, recursive)
      if (ownerChanged) await api.chownPath(entry.path, { user: owner, group }, recursive)
      toast.success(t('files.permissionsSaved', { defaultValue: 'Permissions updated' }))
      onApplied()
      onOpenChange(false)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('files.permissionsFailed', { defaultValue: 'Could not update permissions' }))
    } finally {
      setSaving(false)
    }
  }

  const isLink = !!entry?.linkTarget

  return (
    <Dialog open={!!entry} onOpenChange={(open) => !open && onOpenChange(false)}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('files.permissions')}</DialogTitle>
          <DialogDescription className="break-all">{entry?.path}</DialogDescription>
        </DialogHeader>

        {isLink ? (
          // The server refuses this, and saying why here is better than letting
          // the operator fill the form and get an error on Apply.
          <p className="rounded-xl bg-warning/10 p-3 text-[13px] text-warning">
            {t('files.permissionsSymlink', {
              target: entry?.linkTarget,
              defaultValue: 'This is a link to {{target}}. Change permissions on that file instead.',
            })}
          </p>
        ) : (
          <div className="space-y-4">
            <div className="overflow-hidden rounded-xl border">
              <table className="w-full text-[13px]">
                <thead>
                  <tr className="bg-secondary/40 text-[11px] uppercase tracking-wider text-muted-foreground">
                    <th className="px-3 py-2 text-left font-medium">{t('files.who', { defaultValue: 'Who' })}</th>
                    {WHAT.map((w) => (
                      <th key={w} className="px-3 py-2 text-center font-medium">
                        {t(`files.perm_${w}`, { defaultValue: { r: 'Read', w: 'Write', x: 'Execute' }[w] })}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y">
                  {WHO.map((who, i) => (
                    <tr key={who}>
                      <td className="px-3 py-2">
                        {t(`files.who_${who}`, { defaultValue: { user: 'Owner', group: 'Group', other: 'Everyone' }[who] })}
                      </td>
                      {WHAT.map((what) => (
                        <td key={what} className="px-3 py-2 text-center">
                          <Checkbox
                            checked={(digits[i] & BIT[what]) !== 0}
                            onCheckedChange={() => toggle(i, what)}
                            aria-label={`${who} ${what}`}
                          />
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className="flex items-center gap-2">
              <span className="text-[13px] text-muted-foreground">{t('files.octal', { defaultValue: 'Octal' })}</span>
              <Input
                value={octal}
                onChange={(e) => setOctal(e.target.value.replace(/[^0-7]/g, '').slice(0, 4))}
                className="h-8 w-24 font-mono text-[13px]"
                aria-label={t('files.octal', { defaultValue: 'Octal' })}
              />
            </div>

            <div className="grid grid-cols-2 gap-2">
              <div>
                <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">
                  {t('files.owner', { defaultValue: 'Owner' })}
                </label>
                <Input value={owner} onChange={(e) => setOwner(e.target.value)} className="h-8 font-mono text-[13px]" />
              </div>
              <div>
                <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">
                  {t('files.group', { defaultValue: 'Group' })}
                </label>
                <Input value={group} onChange={(e) => setGroup(e.target.value)} className="h-8 font-mono text-[13px]" />
              </div>
            </div>

            {entry?.isDir && (
              <label className="flex items-center gap-2 text-[13px]">
                <Checkbox checked={recursive} onCheckedChange={(v) => setRecursive(v === true)} />
                {t('files.applyRecursively', { defaultValue: 'Apply to everything inside' })}
              </label>
            )}
          </div>
        )}

        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button
            className="rounded-xl"
            onClick={handleApply}
            disabled={saving || isLink || (!modeChanged && !ownerChanged)}
          >
            {saving && <Loader2 className="animate-spin" />}
            {t('common.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
