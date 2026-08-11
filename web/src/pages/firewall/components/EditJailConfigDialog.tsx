import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Settings, AlertTriangle } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

/** The jail fields the edit form needs (structural subset of Fail2banJail). */
export interface JailConfigTarget {
  name: string
  max_retry: number
  ban_time: string
  find_time: string
  ignoreip: string
}

/** Per-jail config editor — owns its form state; opens when `jail` is set. */
export function EditJailConfigDialog({
  jail,
  onClose,
  onSaved,
}: {
  jail: JailConfigTarget | null
  onClose: () => void
  // Fired after a successful save so the page can refresh the jail list/detail.
  onSaved: (name: string) => void
}) {
  const { t } = useTranslation()

  const [maxRetry, setMaxRetry] = useState('')
  const [banTime, setBanTime] = useState('')
  const [findTime, setFindTime] = useState('')
  const [ignoreIP, setIgnoreIP] = useState('')
  const [loading, setLoading] = useState(false)

  // Populate the form from the target jail each time the dialog opens.
  useEffect(() => {
    if (!jail) return
    setMaxRetry(String(jail.max_retry))
    setBanTime(jail.ban_time)
    setFindTime(jail.find_time)
    setIgnoreIP(jail.ignoreip || '')
  }, [jail])

  const handleSave = async () => {
    if (!jail) return
    try {
      setLoading(true)
      await api.updateFail2banJailConfig(jail.name, {
        max_retry: parseInt(maxRetry, 10),
        ban_time: banTime,
        find_time: findTime,
        ignoreip: ignoreIP,
      })
      toast.success(t('firewall.fail2ban.configUpdated'))
      onClose()
      onSaved(jail.name)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('firewall.fail2ban.configUpdateFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog open={!!jail} onOpenChange={(open) => { if (!loading && !open) onClose() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Settings className="h-4 w-4" />
            {t('firewall.fail2ban.editConfig')}
          </DialogTitle>
          <DialogDescription>
            {t('firewall.fail2ban.editConfigDesc')}
          </DialogDescription>
        </DialogHeader>

        {/* Warning Banner */}
        <div className="bg-warning/10 border border-warning/30 rounded-xl px-4 py-3 flex items-start gap-3">
          <AlertTriangle className="h-5 w-5 text-warning shrink-0 mt-0.5" />
          <p className="text-[13px] text-warning font-medium leading-relaxed">
            {t('firewall.fail2ban.configWarning')}
          </p>
        </div>

        <div className="space-y-4">
          {/* Jail name display */}
          <div>
            <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
              Jail
            </label>
            <p className="text-[13px] font-mono font-medium mt-1">{jail?.name}</p>
          </div>

          {/* Max Retry */}
          <div className="space-y-1.5">
            <label className="text-[13px] font-medium">{t('firewall.fail2ban.maxRetry')}</label>
            <Input
              type="number"
              min={1}
              max={100}
              value={maxRetry}
              onChange={(e) => setMaxRetry(e.target.value)}
              className="rounded-xl text-[13px] font-mono"
            />
            <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.maxRetryHint')}</p>
          </div>

          {/* Ban Time */}
          <div className="space-y-1.5">
            <label className="text-[13px] font-medium">{t('firewall.fail2ban.banTime')}</label>
            <Input
              value={banTime}
              onChange={(e) => setBanTime(e.target.value)}
              className="rounded-xl text-[13px] font-mono"
            />
            <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.banTimeHint')}</p>
          </div>

          {/* Find Time */}
          <div className="space-y-1.5">
            <label className="text-[13px] font-medium">{t('firewall.fail2ban.findTime')}</label>
            <Input
              value={findTime}
              onChange={(e) => setFindTime(e.target.value)}
              className="rounded-xl text-[13px] font-mono"
            />
            <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.findTimeHint')}</p>
          </div>

          {/* Ignore IP */}
          <div className="space-y-1.5">
            <label className="text-[13px] font-medium">{t('firewall.fail2ban.ignoreIp')}</label>
            <Input
              placeholder="127.0.0.1/8 ::1"
              value={ignoreIP}
              onChange={(e) => setIgnoreIP(e.target.value)}
              className="rounded-xl text-[13px] font-mono"
            />
            <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.ignoreIpHelp')}</p>
          </div>
        </div>

        <DialogFooter className="gap-2">
          <Button
            variant="outline"
            onClick={onClose}
            disabled={loading}
            className="rounded-xl"
          >
            {t('common.cancel')}
          </Button>
          <Button
            onClick={handleSave}
            disabled={loading}
            className="rounded-xl"
          >
            {loading && <Loader2 className="h-4 w-4 animate-spin" />}
            {t('common.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
