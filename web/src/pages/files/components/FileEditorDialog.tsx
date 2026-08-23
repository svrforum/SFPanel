import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Loader2, Save } from 'lucide-react'
import { toast } from 'sonner'
import Editor from '@monaco-editor/react'
import '@/lib/monaco' // configures the bundled (non-CDN) Monaco; lazy with the Files page so it stays out of the entry bundle
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { useConfirm } from '@/components/ConfirmDialog'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { getLanguageFromFilename } from './fileLanguages'

export interface EditorTarget {
  path: string
  name: string
}

// Monaco paints into its own surface and cannot inherit the app's CSS tokens,
// so the theme is handed over by name and swapped by hand — the same shape the
// terminal uses. Hardcoding vs-dark left a light-mode operator with one dark
// rectangle in the middle of the page.
function currentEditorTheme() {
  return document.documentElement.classList.contains('dark') ? 'vs-dark' : 'vs'
}

/**
 * Monaco file editor dialog.
 *
 * Loads the file itself when a target opens; a stale load (target changed or
 * dialog closed mid-flight) is discarded, and a load failure toasts and closes.
 *
 * Two properties beyond that are load-bearing, because the dialog used to lose
 * work in four different ways:
 *
 *  - Unsaved edits are tracked, and closing with them pending asks first.
 *    Escape and a backdrop click both went straight through before, discarding
 *    an edit with no prompt and no way back.
 *  - The modification time read with the file is sent back on save, so a file
 *    that changed underneath is refused rather than silently overwritten. Two
 *    tabs on one file used to end with the second save discarding the first
 *    and the loser never finding out.
 */
export function FileEditorDialog({ target, onOpenChange, onSaved }: {
  /** File to edit; null keeps the dialog closed. */
  target: EditorTarget | null
  onOpenChange: (open: boolean) => void
  /** Called after a successful save so the caller can refresh its listing. */
  onSaved?: () => void
}) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [theme, setTheme] = useState(currentEditorTheme)
  // The text as it was loaded, and the timestamp it carried. Dirty is a
  // comparison rather than a flag so an edit-and-undo does not count as
  // unsaved work and nag on close.
  const loadedRef = useRef('')
  const modTimeRef = useRef<string | undefined>(undefined)
  const dirty = !loading && content !== loadedRef.current

  // Reset before paint when a new file opens (adjust-state-during-render, same
  // pattern as TypeToConfirmDialog) so the previous file's content never flashes.
  const [prevTarget, setPrevTarget] = useState<EditorTarget | null>(target)
  if (prevTarget !== target) {
    setPrevTarget(target)
    if (target) {
      setContent('')
      loadedRef.current = ''
      modTimeRef.current = undefined
      setLoading(true)
    }
  }

  useEffect(() => {
    const apply = () => setTheme(currentEditorTheme())
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    window.addEventListener('sfpanel:themechange', apply)
    media.addEventListener('change', apply)
    return () => {
      window.removeEventListener('sfpanel:themechange', apply)
      media.removeEventListener('change', apply)
    }
  }, [])

  useEffect(() => {
    if (!target) return
    let cancelled = false
    setLoading(true)
    api.readFile(target.path)
      .then((data) => {
        if (cancelled) return
        setContent(data.content || '')
        loadedRef.current = data.content || ''
        modTimeRef.current = data.modTime
      })
      .catch((err: unknown) => {
        if (cancelled) return
        toast.error(err instanceof Error ? err.message : t('files.readFailed'))
        onOpenChange(false)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
    // The load is keyed to the opened file only; t/onOpenChange are stable enough.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target])

  const handleSave = useCallback(async (force = false) => {
    if (!target) return
    setSaving(true)
    try {
      await api.writeFile(target.path, content, {
        expectModTime: force ? undefined : modTimeRef.current,
      })
      toast.success(t('files.saveSuccess'))
      loadedRef.current = content
      onSaved?.()
      onOpenChange(false)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.saveFailed')
      // The server refuses a save whose file moved on. Offer the choice rather
      // than either discarding the edit or overwriting somebody's work.
      if (/changed on disk|no longer exists/i.test(message)) {
        const proceed = await confirm({
          title: t('files.conflictTitle', { defaultValue: 'This file changed on disk' }),
          description: t('files.conflictBody', {
            defaultValue:
              'Someone or something modified this file after you opened it. Saving now replaces their version with yours.',
          }),
          confirmLabel: t('files.conflictOverwrite', { defaultValue: 'Overwrite anyway' }),
          danger: true,
        })
        if (proceed) await handleSave(true)
        return
      }
      toast.error(message)
    } finally {
      setSaving(false)
    }
    // handleSave recurses on the force path; the deps are the values it reads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, content, t, onOpenChange, onSaved])

  // Ctrl+S was unbound, so the reflex opened the browser's Save Page dialog
  // over an unsaved editor.
  useEffect(() => {
    if (!target) return
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
        e.preventDefault()
        if (!saving && !loading) void handleSave()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [target, saving, loading, handleSave])

  const requestClose = useCallback(async () => {
    if (!dirty) {
      onOpenChange(false)
      return
    }
    const discard = await confirm({
      title: t('files.discardTitle', { defaultValue: 'Discard unsaved changes?' }),
      description: t('files.discardBody', {
        defaultValue: 'This file has edits that have not been saved. Closing loses them.',
      }),
      confirmLabel: t('files.discardConfirm', { defaultValue: 'Discard' }),
      danger: true,
    })
    if (discard) onOpenChange(false)
  }, [dirty, confirm, onOpenChange, t])

  return (
    <Dialog open={!!target} onOpenChange={(open) => { if (!open) void requestClose() }}>
      <DialogContent className="sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {t('files.editFile')}
            {dirty && (
              <span className="text-[11px] font-normal text-warning">
                {t('files.unsaved', { defaultValue: 'unsaved' })}
              </span>
            )}
          </DialogTitle>
          <DialogDescription className="break-all">
            {target?.path}
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="flex items-center justify-center py-16">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            <span className="ml-2 text-muted-foreground">{t('files.loadingFile')}</span>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="rounded-xl overflow-hidden border">
              <Editor
                height="min(60vh, 500px)"
                language={getLanguageFromFilename(target?.name ?? '')}
                theme={theme}
                value={content}
                onChange={(val) => setContent(val || '')}
                options={{
                  minimap: { enabled: false },
                  fontSize: 14,
                  lineNumbers: 'on',
                  scrollBeyondLastLine: false,
                  wordWrap: 'on',
                  tabSize: 2,
                  insertSpaces: true,
                  automaticLayout: true,
                }}
              />
            </div>
            <DialogFooter className="gap-2 sm:gap-2">
              <span className="mr-auto hidden items-center gap-1 text-[11px] text-muted-foreground sm:flex">
                <AlertTriangle className="h-3 w-3" aria-hidden="true" />
                {t('files.saveHint', { defaultValue: 'Ctrl+S saves' })}
              </span>
              <Button variant="outline" className="rounded-xl" onClick={() => void requestClose()}>
                {t('common.cancel')}
              </Button>
              <Button className="rounded-xl" onClick={() => void handleSave()} disabled={saving}>
                {saving ? (
                  <>
                    <Loader2 className="animate-spin" />
                    {t('common.saving')}
                  </>
                ) : (
                  <>
                    <Save />
                    {t('common.save')}
                  </>
                )}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
