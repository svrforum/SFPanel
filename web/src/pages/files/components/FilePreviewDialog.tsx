import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Download, FileQuestion, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { formatBytes } from '@/lib/utils'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import type { FileKind } from '@/types/api'

export interface PreviewTarget {
  path: string
  name: string
  size: number
  kind: FileKind
}

// Images fetched for preview are capped well below the download limit. A
// preview is a glance, not a transfer: pulling a 200 MB camera RAW into a blob
// URL to show a thumbnail would stall the tab for no benefit, and the download
// button next to it does that job properly.
const previewMaxBytes = 20 * 1024 * 1024

/**
 * Shows what a file is when it is not text.
 *
 * Every non-directory used to open in Monaco. A PNG rendered as a screen of
 * replacement characters with an enabled Save button, and pressing it wrote
 * those characters over the original — so this dialog is not a nicety, it is
 * the alternative to a file being destroyed by two clicks.
 */
export function FilePreviewDialog({ target, onOpenChange, onDownload }: {
  target: PreviewTarget | null
  onOpenChange: (open: boolean) => void
  onDownload: (target: PreviewTarget) => void
}) {
  const { t } = useTranslation()
  const [url, setUrl] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  // Clear the previous image before paint when the target changes, so one
  // file's preview never flashes under another's name. Same
  // adjust-state-during-render shape the editor dialog uses; doing it inside
  // the effect would be a cascading render.
  const [prevTarget, setPrevTarget] = useState<PreviewTarget | null>(target)
  if (prevTarget !== target) {
    setPrevTarget(target)
    setUrl(null)
    setLoading(!!target && target.kind === 'image' && target.size <= previewMaxBytes)
  }

  useEffect(() => {
    if (!target || target.kind !== 'image' || target.size > previewMaxBytes) {
      return
    }
    let cancelled = false
    let objectUrl: string | null = null
    api.downloadFile(target.path)
      .then((blob) => {
        if (cancelled) return
        objectUrl = URL.createObjectURL(blob)
        setUrl(objectUrl)
      })
      .catch((err: unknown) => {
        if (!cancelled) toast.error(err instanceof Error ? err.message : t('files.readFailed'))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
      // Revoke on the way out or every preview leaks its blob for the life of
      // the tab, which on an image-heavy directory is real memory.
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [target, t])

  const tooLarge = !!target && target.kind === 'image' && target.size > previewMaxBytes

  return (
    <Dialog open={!!target} onOpenChange={(open) => !open && onOpenChange(false)}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="break-all">{target?.name}</DialogTitle>
          <DialogDescription className="break-all">
            {target ? `${target.path} · ${formatBytes(target.size)}` : ''}
          </DialogDescription>
        </DialogHeader>

        <div className="flex min-h-[200px] items-center justify-center rounded-xl bg-secondary/40 p-4">
          {loading ? (
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" aria-hidden="true" />
          ) : url ? (
            <img
              src={url}
              alt={target?.name ?? ''}
              className="max-h-[60vh] max-w-full rounded-lg object-contain"
            />
          ) : (
            <div className="flex flex-col items-center gap-2 py-8 text-center">
              <FileQuestion className="h-8 w-8 text-muted-foreground" aria-hidden="true" />
              <p className="text-[13px] text-muted-foreground">
                {tooLarge
                  ? t('files.previewTooLarge', { defaultValue: 'Too large to preview — download it to open it.' })
                  : t('files.previewBinary', { defaultValue: 'This file is not text and cannot be shown here.' })}
              </p>
            </div>
          )}
        </div>

        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            {t('common.close')}
          </Button>
          <Button
            className="rounded-xl"
            onClick={() => { if (target) onDownload(target) }}
          >
            <Download className="h-4 w-4" />
            {t('files.download')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
