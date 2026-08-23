import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronRight, Folder, Home, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

/**
 * Choose a destination directory by browsing to it.
 *
 * Copy already existed, but its destination was a free-text field: the
 * operator typed an absolute path from memory into a prompt, and a typo
 * produced either a 404 or — worse — a file created in a directory they did
 * not mean. Move did not exist at all in the UI even though the server has
 * always supported it, because rename joined the new name onto the current
 * directory and so could never leave it.
 *
 * Browsing is the fix for both. The typed field stays, for the operator who
 * knows exactly where they are going, but it is no longer the only way.
 */
export function FolderPickerDialog({ open, title, description, initialPath, confirmLabel, onConfirm, onOpenChange }: {
  open: boolean
  title: string
  description?: string
  initialPath: string
  confirmLabel: string
  onConfirm: (path: string) => void
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const [path, setPath] = useState(initialPath)
  const [folders, setFolders] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [typed, setTyped] = useState(initialPath)

  // Reset to the caller's starting point each time the dialog opens, rather
  // than resuming wherever the last move happened to end.
  const [wasOpen, setWasOpen] = useState(open)
  if (wasOpen !== open) {
    setWasOpen(open)
    if (open) {
      setPath(initialPath)
      setTyped(initialPath)
      setLoading(true)
    }
  }

  const load = useCallback(async (target: string) => {
    setLoading(true)
    try {
      const entries = await api.listFiles(target)
      setFolders((entries || []).filter((e) => e.isDir).map((e) => e.name))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('files.fetchFailed'))
      setFolders([])
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    if (!open) return
    void load(path)
  }, [open, path, load])

  const goTo = (next: string) => {
    setPath(next)
    setTyped(next)
  }

  const segments = path.split('/').filter(Boolean)
  const parent = segments.length > 0 ? '/' + segments.slice(0, -1).join('/') : null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription className="break-all">{description}</DialogDescription>}
        </DialogHeader>

        <div className="space-y-3">
          {/* Typed path stays available — browsing is the addition, not a
              replacement for someone who knows the path. */}
          <Input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                goTo(typed.trim() || '/')
              }
            }}
            className="h-9 font-mono text-[13px]"
            aria-label={t('files.destination', { defaultValue: 'Destination folder' })}
          />

          <div className="flex items-center gap-1 text-[12px] text-muted-foreground">
            <button
              type="button"
              onClick={() => goTo('/')}
              className="rounded-sm p-0.5 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/40"
              aria-label={t('files.root', { defaultValue: 'Root' })}
            >
              <Home className="h-3.5 w-3.5" />
            </button>
            <span className="truncate font-mono">{path}</span>
          </div>

          <div className="h-56 overflow-y-auto rounded-xl border">
            {loading ? (
              <div className="flex h-full items-center justify-center">
                <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" aria-hidden="true" />
              </div>
            ) : (
              <ul className="divide-y">
                {parent !== null && (
                  <li>
                    <button
                      type="button"
                      onClick={() => goTo(parent || '/')}
                      className="flex w-full items-center gap-2 px-3 py-2.5 text-left text-[13px] outline-none hover:bg-secondary/50 focus-visible:bg-secondary/50"
                    >
                      <Folder className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                      <span className="text-muted-foreground">..</span>
                    </button>
                  </li>
                )}
                {folders.length === 0 && parent === null && (
                  <li className="px-3 py-8 text-center text-[13px] text-muted-foreground">
                    {t('files.noSubfolders', { defaultValue: 'No folders here' })}
                  </li>
                )}
                {folders.map((name) => (
                  <li key={name}>
                    <button
                      type="button"
                      onClick={() => goTo(path === '/' ? `/${name}` : `${path}/${name}`)}
                      className="flex w-full items-center gap-2 px-3 py-2.5 text-left text-[13px] outline-none hover:bg-secondary/50 focus-visible:bg-secondary/50"
                    >
                      <Folder className="h-4 w-4 shrink-0 text-blue-500" aria-hidden="true" />
                      <span className="truncate">{name}</span>
                      <ChevronRight className="ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>

        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button className="rounded-xl" onClick={() => onConfirm(typed.trim() || path)}>
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
