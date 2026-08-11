import { useTranslation } from 'react-i18next'
import { ShieldAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

/** "What is Fail2ban" explainer — shown from both the not-installed and
 * installed views (which used to carry two copies of this JSX). */
export function Fail2banAboutDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldAlert className="h-5 w-5 text-primary" />
            {t('firewall.fail2ban.aboutTitle')}
          </DialogTitle>
          <DialogDescription>
            {t('firewall.fail2ban.aboutDesc')}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {/* How it works */}
          <div className="space-y-2">
            <h4 className="text-[13px] font-semibold">{t('firewall.fail2ban.aboutHowTitle')}</h4>
            <ol className="space-y-1.5 text-[13px] text-muted-foreground list-decimal list-inside">
              <li>{t('firewall.fail2ban.aboutHow1')}</li>
              <li>{t('firewall.fail2ban.aboutHow2')}</li>
              <li>{t('firewall.fail2ban.aboutHow3')}</li>
            </ol>
          </div>

          {/* Jail types */}
          <div className="space-y-2">
            <h4 className="text-[13px] font-semibold">{t('firewall.fail2ban.aboutJailTitle')}</h4>
            <ul className="space-y-1.5 text-[13px] text-muted-foreground">
              <li className="flex items-start gap-2">
                <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-primary/10 text-primary shrink-0 mt-0.5">sshd</span>
                <span>{t('firewall.fail2ban.aboutJailSSH')}</span>
              </li>
              <li className="flex items-start gap-2">
                <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-secondary text-muted-foreground shrink-0 mt-0.5">nginx</span>
                <span>{t('firewall.fail2ban.aboutJailNginx')}</span>
              </li>
              <li className="flex items-start gap-2">
                <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-secondary text-muted-foreground shrink-0 mt-0.5">apache</span>
                <span>{t('firewall.fail2ban.aboutJailApache')}</span>
              </li>
              <li className="flex items-start gap-2">
                <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-secondary text-muted-foreground shrink-0 mt-0.5">recidive</span>
                <span>{t('firewall.fail2ban.aboutJailRecidive')}</span>
              </li>
            </ul>
          </div>

          {/* Recommendation */}
          <div className="bg-primary/5 rounded-xl px-4 py-3">
            <p className="text-[13px] text-primary font-medium">
              {t('firewall.fail2ban.aboutRecommend')}
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button onClick={() => onOpenChange(false)} className="rounded-xl">
            {t('common.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
