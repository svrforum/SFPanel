import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { AITools } from '@/types/api'
import { Button } from '@/components/ui/button'

// Sessions need tmux; hosts without it get one button that uses the ordinary
// apt install route. The systemd-run note is informational — sessions still
// work, they just do not outlive a panel restart.
export function TmuxBanner({ tools, onChanged }: { tools: AITools | null; onChanged: () => void }) {
  const { t } = useTranslation()
  const [installing, setInstalling] = useState(false)
  if (!tools) return null
  if (tools.tmux.installed) {
    return tools.systemd_run ? null : (
      <p className="text-[12px] text-muted-foreground flex items-center gap-1.5">
        <AlertTriangle className="h-3.5 w-3.5 text-warning" aria-hidden="true" />{t('ai.tmux.noService')}
      </p>
    )
  }
  const install = async () => {
    setInstalling(true)
    try {
      await api.installPackage('tmux')
      toast.success(t('ai.tmux.installed'))
      onChanged()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('ai.errors.generic'))
    } finally {
      setInstalling(false)
    }
  }
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-warning/30 bg-warning/10 px-4 py-3">
      <p className="text-[13px] flex items-center gap-2"><AlertTriangle className="h-4 w-4 text-warning shrink-0" aria-hidden="true" />{t('ai.tmux.missing')}</p>
      <Button size="sm" className="rounded-xl" onClick={install} disabled={installing}>
        {installing ? <><Loader2 className="animate-spin" aria-hidden="true" />{t('ai.tmux.installing')}</> : t('ai.tmux.install')}
      </Button>
    </div>
  )
}
