import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { validateFrom } from './ruleValidation'
import { useLockoutGuard } from './useLockoutGuard'

export interface PortRuleTarget {
  port: number
  proto: 'tcp' | 'udp'
}

/** A ufw rule for one listening port: its action, an optional source and a
 * comment. Open while `target` is set; `onAdded` follows a rule the server
 * took, forced or not. */
export function AddPortRuleDialog({ target, onClose, onAdded }: { target: PortRuleTarget | null; onClose: () => void; onAdded?: () => void }) {
  const { t } = useTranslation()
  const [ruleAction, setRuleAction] = useState<string>('allow')
  const [ruleFrom, setRuleFrom] = useState<string>('')
  const [ruleComment, setRuleComment] = useState<string>('')
  const [adding, setAdding] = useState(false)

  // A new target starts a blank form (adjust-state-during-render). The last
  // one opened outlives `target`, so the closing animation still names it.
  const [prevTarget, setPrevTarget] = useState<PortRuleTarget | null>(target)
  const [shown, setShown] = useState<PortRuleTarget | null>(target)
  if (prevTarget !== target) {
    setPrevTarget(target)
    if (target) {
      setShown(target)
      setRuleAction('allow')
      setRuleFrom('')
      setRuleComment('')
    }
  }
  const title = shown ? t('firewall.ports.addRuleFor', { port: shown.port, proto: shown.proto.toUpperCase() }) : ''

  // Same source-IP validation as the FirewallRules dialogs — both entry points
  // feed the same addFirewallRule API.
  const fromError = !!ruleFrom.trim() && !validateFrom(ruleFrom)

  // The rules page's lockout prompt: denying a port here is refused the same
  // way, and it was only a toast with no way to go ahead.
  const { handleLockoutError, clearLockout, lockoutDialog } = useLockoutGuard()

  const handleAddClick = () => {
    if (fromError) {
      document.getElementById('port-rule-from')?.focus()
      return
    }
    void runAddRule(false)
  }

  const runAddRule = async (force: boolean) => {
    if (!target || fromError) return
    setAdding(true)
    try {
      await api.addFirewallRule({
        action: ruleAction,
        port: String(target.port),
        protocol: target.proto,
        from: ruleFrom.trim() || '',
        to: '',
        comment: ruleComment.trim() || '',
      }, force)
      toast.success(`${t('firewall.ports.addRule')} — ${target.port}/${target.proto.toUpperCase()}`)
      onAdded?.()
      onClose()
      clearLockout()
    } catch (err: unknown) {
      if (!handleLockoutError(err, () => runAddRule(true))) {
        toast.error(err instanceof Error ? err.message : t('common.error'))
      }
    } finally {
      setAdding(false)
    }
  }

  return (
    <>
      <Dialog open={!!target} onOpenChange={(open) => { if (!open) onClose() }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            {/* Action selector */}
            <div className="space-y-1.5">
              <label htmlFor="port-rule-action" className="text-[13px] font-medium">{t('firewall.rules.action')}</label>
              <Select value={ruleAction} onValueChange={setRuleAction}>
                <SelectTrigger id="port-rule-action" className="rounded-xl">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="allow">{t('firewall.rules.allow')}</SelectItem>
                  <SelectItem value="deny">{t('firewall.rules.deny')}</SelectItem>
                  <SelectItem value="reject">{t('firewall.rules.reject')}</SelectItem>
                  <SelectItem value="limit">{t('firewall.rules.limit')}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {/* Source IP */}
            <div className="space-y-1.5">
              <label htmlFor="port-rule-from" className="text-[13px] font-medium">{t('firewall.rules.fromIP')}</label>
              <Input
                id="port-rule-from"
                value={ruleFrom}
                onChange={(e) => setRuleFrom(e.target.value)}
                placeholder={t('firewall.rules.any')}
                aria-invalid={fromError || undefined}
                aria-describedby="port-rule-from-hint"
                className="rounded-xl text-base md:text-[13px]"
              />
              {fromError ? (
                <p id="port-rule-from-hint" className="text-[12px] text-destructive">{t('firewall.rules.invalidIP')}</p>
              ) : (
                <p id="port-rule-from-hint" className="text-[12px] text-muted-foreground">{t('firewall.rules.fromIPHint')}</p>
              )}
            </div>

            {/* Comment */}
            <div className="space-y-1.5">
              <label htmlFor="port-rule-comment" className="text-[13px] font-medium">{t('firewall.rules.comment')}</label>
              <Input
                id="port-rule-comment"
                value={ruleComment}
                onChange={(e) => setRuleComment(e.target.value)}
                placeholder=""
                className="rounded-xl text-base md:text-[13px]"
              />
            </div>
          </div>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={onClose} className="rounded-xl">
              {t('common.cancel')}
            </Button>
            <Button onClick={handleAddClick} disabled={adding} className="rounded-xl">
              {adding && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('firewall.rules.addRule')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {lockoutDialog}
    </>
  )
}
