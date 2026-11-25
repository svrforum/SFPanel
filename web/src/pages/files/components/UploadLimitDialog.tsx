import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { NodeSettingField } from '@/components/NodeSettingField'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useServerScope } from '@/hooks/useServerScope'
import { UPLOAD_LIMIT_MAX_MB } from '../uploadLimit'

/**
 * The largest file this server accepts, set where uploads happen. The field
 * mounts with the dialog, so the value is read when it opens, from the server
 * the page is showing. The server takes 1–1024 MB; the hint says so and names
 * the server, since the dialog covers the line above the page that does.
 */
export function UploadLimitDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation()
  const { currentName } = useServerScope()
  // Radix hands the focus back to a DialogTrigger, and this dialog has none:
  // it opens from the Upload button's menu. Closing it returns the focus to
  // what had it when it opened, while that is still on the page.
  const openedFrom = useRef<HTMLElement | null>(null)
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* The field's own hint describes it; the dialog has nothing to add. */}
      <DialogContent
        className="sm:max-w-md"
        aria-describedby={undefined}
        onOpenAutoFocus={() => { openedFrom.current = document.activeElement instanceof HTMLElement ? document.activeElement : null }}
        onCloseAutoFocus={(e) => {
          const target = openedFrom.current
          openedFrom.current = null
          if (!target?.isConnected) return
          e.preventDefault()
          target.focus()
        }}
      >
        <DialogHeader>
          <DialogTitle>{t('files.uploadLimit.title')}</DialogTitle>
        </DialogHeader>
        <NodeSettingField
          settingKey="max_upload_size"
          id="max-upload-size"
          label={t('files.uploadLimit.label')}
          unit="MB"
          hint={currentName ? t('files.uploadLimit.hintNamed', { name: currentName }) : t('files.uploadLimit.hint')}
          min={1}
          max={UPLOAD_LIMIT_MAX_MB}
          invalid={t('files.uploadLimit.invalid')}
        />
      </DialogContent>
    </Dialog>
  )
}
