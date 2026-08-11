import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Save } from 'lucide-react'
import { toast } from 'sonner'
import Editor from '@monaco-editor/react'
import '@/lib/monaco' // configures the bundled (non-CDN) Monaco; lazy with the Files page so it stays out of the entry bundle
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
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

/**
 * Monaco file editor dialog. Loads the file itself when a target opens; a
 * stale load (target changed or dialog closed mid-flight) is discarded, and a
 * load failure toasts and closes the dialog (previous behaviour).
 */
export function FileEditorDialog({ target, onOpenChange }: {
  /** File to edit; null keeps the dialog closed. */
  target: EditorTarget | null
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  // Reset before paint when a new file opens (adjust-state-during-render, same
  // pattern as TypeToConfirmDialog) so the previous file's content never flashes.
  const [prevTarget, setPrevTarget] = useState<EditorTarget | null>(target)
  if (prevTarget !== target) {
    setPrevTarget(target)
    if (target) {
      setContent('')
      setLoading(true)
    }
  }

  useEffect(() => {
    if (!target) return
    let cancelled = false
    setLoading(true)
    api.readFile(target.path)
      .then((data) => {
        if (!cancelled) setContent(data.content || '')
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

  const handleSave = async () => {
    if (!target) return
    setSaving(true)
    try {
      await api.writeFile(target.path, content)
      toast.success(t('files.saveSuccess'))
      onOpenChange(false)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('files.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={!!target} onOpenChange={(open) => !open && onOpenChange(false)}>
      <DialogContent className="sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>{t('files.editFile')}</DialogTitle>
          <DialogDescription>
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
            <div className="rounded-md overflow-hidden border">
              <Editor
                height="500px"
                language={getLanguageFromFilename(target?.name ?? '')}
                theme="vs-dark"
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
            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                {t('common.cancel')}
              </Button>
              <Button onClick={handleSave} disabled={saving}>
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
