import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copyText } from '@/lib/utils'

// The terminal's output as plain text a phone can select. xterm draws into a
// canvas, so there is nothing for a long press to select on the terminal
// itself, and a drag there scrolls.
//
// `text` is a snapshot, null while it is still being fetched. It outlives
// `open`, so the closing animation shows the text rather than the empty state.
// `returnFocus` puts the keyboard back in the terminal on a desktop; on a phone
// focus is left alone, since the terminal taking it raises the keyboard over
// whatever the operator just copied the text to paste into.
export function SelectTextDialog({ open, text, onClose, returnFocus }: {
  open: boolean
  text: string | null
  onClose: () => void
  returnFocus: () => void
}) {
  const { t } = useTranslation()
  // Open on the latest output, where the thing worth copying usually is. A
  // callback ref, not an effect on `text`: the dialog's portal mounts its
  // content a render after the text arrives, when an effect has already run
  // against no element.
  const scrollToEnd = useCallback((pre: HTMLPreElement | null) => {
    if (pre) pre.scrollTop = pre.scrollHeight
  }, [])

  const copyAll = async () => {
    if (!text) return
    if (await copyText(text)) {
      toast.success(t('terminal.selectText.copied'))
      onClose()
    } else {
      toast.error(t('terminal.copyFailed', { defaultValue: 'Could not copy to clipboard' }))
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogContent
        className="sm:max-w-2xl h-[calc(100dvh-2rem)] sm:h-[80dvh] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden"
        onCloseAutoFocus={(e) => {
          e.preventDefault()
          if (!window.matchMedia('(pointer: coarse)').matches) returnFocus()
        }}
      >
        <DialogHeader>
          <DialogTitle>{t('terminal.selectText.title')}</DialogTitle>
          <DialogDescription>{t('terminal.selectText.hint')}</DialogDescription>
        </DialogHeader>
        {text === null ? (
          <p className="flex items-center gap-2 text-[13px] text-muted-foreground" role="status">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />{t('common.loading')}
          </p>
        ) : text ? (
          <pre
            ref={scrollToEnd}
            tabIndex={0}
            aria-label={t('terminal.selectText.title')}
            className="min-h-0 overflow-auto overscroll-contain rounded-xl bg-secondary/50 p-3 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 font-mono text-[12px] leading-relaxed whitespace-pre-wrap break-all select-text"
          >
            {text}
          </pre>
        ) : (
          <p className="text-[13px] text-muted-foreground">{t('terminal.selectText.empty')}</p>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" className="rounded-xl" onClick={onClose}>{t('common.close')}</Button>
          <Button type="button" className="rounded-xl" onClick={() => { void copyAll() }} disabled={!text}>
            <Copy aria-hidden="true" />
            {t('terminal.selectText.copyAll')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
