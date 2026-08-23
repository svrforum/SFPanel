import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Folder, FileText, Loader2, RotateCcw, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { useConfirm } from '@/components/ConfirmDialog'
import { formatBytes, formatDate } from '@/lib/utils'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import type { TrashEntry } from '@/types/api'

/**
 * What was deleted, and the way back.
 *
 * Delete used to be immediate and total — os.RemoveAll on a tree with no undo
 * and no record of what had just gone. On a mounted disk or a network share
 * that is somebody's only copy. This is the other half of the trash: without a
 * place to see it, a recoverable delete is only recoverable by someone who
 * knows the directory exists.
 */
export function TrashDialog({ open, onOpenChange, onRestored }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onRestored: () => void
}) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [entries, setEntries] = useState<TrashEntry[]>([])
  const [retentionDays, setRetentionDays] = useState(7)
  const [loading, setLoading] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await api.listTrash()
      setEntries(data.entries || [])
      setRetentionDays(data.retentionDays || 7)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('files.trashLoadFailed', { defaultValue: 'Could not read the trash' }))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  const handleRestore = async (entry: TrashEntry) => {
    setBusyId(entry.id)
    try {
      const result = await api.restoreFromTrash(entry.id)
      toast.success(t('files.restored', { path: result.restored, defaultValue: 'Restored to {{path}}' }))
      await load()
      onRestored()
    } catch (err: unknown) {
      const status = (err as { status?: number })?.status
      // The server refuses to restore over something, because clobbering a
      // newer file to recover an older one is the opposite of what was asked.
      toast.error(status === 409
        ? t('files.restoreExists', { defaultValue: 'Something already exists at the original path' })
        : (err instanceof Error ? err.message : t('files.restoreFailed', { defaultValue: 'Could not restore' })))
    } finally {
      setBusyId(null)
    }
  }

  const handlePurge = async (entry: TrashEntry) => {
    const ok = await confirm({
      title: t('files.purgeTitle', { defaultValue: 'Delete permanently?' }),
      description: t('files.purgeBody', { name: entry.name, defaultValue: '{{name}} cannot be recovered after this.' }),
      confirmLabel: t('files.purge', { defaultValue: 'Delete permanently' }),
      danger: true,
    })
    if (!ok) return
    setBusyId(entry.id)
    try {
      await api.purgeTrash(entry.id)
      await load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('files.purgeFailed', { defaultValue: 'Could not empty' }))
    } finally {
      setBusyId(null)
    }
  }

  const handleEmpty = async () => {
    const ok = await confirm({
      title: t('files.emptyTrashTitle', { defaultValue: 'Empty the trash?' }),
      description: t('files.emptyTrashBody', {
        count: entries.length,
        defaultValue: '{{count}} entries will be gone for good.',
      }),
      confirmLabel: t('files.emptyTrash', { defaultValue: 'Empty trash' }),
      danger: true,
    })
    if (!ok) return
    try {
      await api.purgeTrash()
      await load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('files.purgeFailed', { defaultValue: 'Could not empty' }))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('files.trash', { defaultValue: 'Trash' })}</DialogTitle>
          <DialogDescription>
            {t('files.trashRetention', {
              days: retentionDays,
              defaultValue: 'Deleted files are kept for {{days}} days, then removed automatically.',
            })}
          </DialogDescription>
        </DialogHeader>

        <div className="max-h-[50vh] overflow-y-auto rounded-xl border">
          {loading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" aria-hidden="true" />
            </div>
          ) : entries.length === 0 ? (
            <p className="py-12 text-center text-[13px] text-muted-foreground">
              {t('files.trashEmpty', { defaultValue: 'The trash is empty' })}
            </p>
          ) : (
            <ul className="divide-y">
              {entries.map((entry) => (
                <li key={entry.id} className="flex items-center gap-3 px-3 py-2.5">
                  {entry.isDir
                    ? <Folder className="h-4 w-4 shrink-0 text-blue-500" aria-hidden="true" />
                    : <FileText className="h-4 w-4 shrink-0 text-amber-500" aria-hidden="true" />}
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[13px] font-medium">{entry.name}</p>
                    {/* The original path is what makes the entry identifiable:
                        three files called config.yml are otherwise the same row
                        three times. */}
                    <p className="truncate font-mono text-[11px] text-muted-foreground">{entry.originalPath}</p>
                  </div>
                  <span className="hidden shrink-0 text-[11px] text-muted-foreground sm:block">
                    {formatDate(entry.deletedAt)}
                  </span>
                  {!entry.isDir && (
                    <span className="hidden shrink-0 text-[11px] text-muted-foreground sm:block">
                      {formatBytes(entry.size)}
                    </span>
                  )}
                  <div className="flex shrink-0 items-center gap-1">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('files.restore', { defaultValue: 'Restore' })}
                      aria-label={t('files.restore', { defaultValue: 'Restore' })}
                      disabled={busyId === entry.id || !entry.originalPath}
                      onClick={() => void handleRestore(entry)}
                    >
                      {busyId === entry.id ? <Loader2 className="animate-spin" /> : <RotateCcw />}
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('files.purge', { defaultValue: 'Delete permanently' })}
                      aria-label={t('files.purge', { defaultValue: 'Delete permanently' })}
                      disabled={busyId === entry.id}
                      onClick={() => void handlePurge(entry)}
                    >
                      <Trash2 className="text-destructive" />
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>

        <DialogFooter className="gap-2 sm:gap-2">
          <Button
            variant="outline"
            className="mr-auto rounded-xl"
            disabled={entries.length === 0}
            onClick={() => void handleEmpty()}
          >
            <Trash2 className="h-4 w-4" />
            {t('files.emptyTrash', { defaultValue: 'Empty trash' })}
          </Button>
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            {t('common.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
