import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

interface TypeToConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  /** The exact string the operator must type to enable the confirm button (e.g. a device or cluster name). */
  confirmPhrase: string
  confirmLabel: string
  loading?: boolean
  onConfirm: () => void
}

/**
 * TypeToConfirmDialog guards an irreversible action (disk format, partition /
 * RAID delete, cluster disband) by requiring the operator to type an exact
 * phrase — the device or cluster name — before the destructive button enables.
 * Reusable across every "this can't be undone" flow so the friction is
 * consistent and a stray click can't trigger data loss.
 */
export function TypeToConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmPhrase,
  confirmLabel,
  loading,
  onConfirm,
}: TypeToConfirmDialogProps) {
  const { t } = useTranslation()
  const [typed, setTyped] = useState('')

  // Reset the field whenever the dialog opens or closes so each appearance
  // starts blank. This is the "adjust state during render on prop change"
  // pattern (tracking the previous `open`) — it avoids a setState-in-effect and
  // re-renders immediately before paint.
  const [prevOpen, setPrevOpen] = useState(open)
  if (prevOpen !== open) {
    setPrevOpen(open)
    setTyped('')
  }

  const matches = typed === confirmPhrase

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-destructive">
            <AlertTriangle className="h-5 w-5 shrink-0" />
            {title}
          </DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>

        <div className="space-y-2">
          <p className="text-sm text-muted-foreground">
            {t('common.typeToConfirm.prompt')}{' '}
            <span className="font-mono font-semibold text-foreground">{confirmPhrase}</span>
          </p>
          <Input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder={confirmPhrase}
            className="font-mono"
            autoFocus
            spellCheck={false}
            autoComplete="off"
          />
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={loading}
          >
            {t('common.cancel')}
          </Button>
          <Button
            variant="destructive"
            disabled={!matches}
            loading={loading}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
