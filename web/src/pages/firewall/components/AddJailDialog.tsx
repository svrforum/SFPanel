import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Plus, Check } from 'lucide-react'
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

interface JailTemplate {
  id: string
  name: string
  description: string
  filter: string
  log_path: string
  max_retry: number
  ban_time: number
  find_time: number
  available: boolean
}

/** Template-grid + custom-jail creation dialog — owns all its form state;
 * the page only opens it and refetches on success. */
export function AddJailDialog({
  open,
  onOpenChange,
  activeJails,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Names of currently configured jails, to label unavailable templates. */
  activeJails: string[]
  onCreated: () => void
}) {
  const { t } = useTranslation()

  const [templates, setTemplates] = useState<JailTemplate[]>([])
  const [templatesLoading, setTemplatesLoading] = useState(false)
  const [selectedTemplate, setSelectedTemplate] = useState<JailTemplate | null>(null)
  const [isCustomMode, setIsCustomMode] = useState(false)
  const [customName, setCustomName] = useState('')
  const [customFilter, setCustomFilter] = useState('')
  const [newMaxRetry, setNewMaxRetry] = useState('')
  const [newBanTime, setNewBanTime] = useState('')
  const [newFindTime, setNewFindTime] = useState('')
  const [newLogPath, setNewLogPath] = useState('')
  const [newIgnoreIP, setNewIgnoreIP] = useState('')
  const [addJailLoading, setAddJailLoading] = useState(false)

  // Reset selection and (re)load templates each time the dialog opens.
  useEffect(() => {
    if (!open) return
    setSelectedTemplate(null)
    setIsCustomMode(false)
    setCustomName('')
    setCustomFilter('')
    ;(async () => {
      try {
        setTemplatesLoading(true)
        const data = await api.getFail2banTemplates()
        setTemplates(data.templates || [])
      } catch {
        toast.error('Failed to load templates')
      } finally {
        setTemplatesLoading(false)
      }
    })()
  }, [open])

  const handleSelectTemplate = useCallback((tmpl: JailTemplate) => {
    setSelectedTemplate(tmpl)
    setIsCustomMode(false)
    setNewMaxRetry(String(tmpl.max_retry))
    setNewBanTime(String(tmpl.ban_time))
    setNewFindTime(String(tmpl.find_time))
    setNewLogPath(tmpl.log_path)
    setNewIgnoreIP('')
  }, [])

  const handleSelectCustom = useCallback(() => {
    setSelectedTemplate(null)
    setIsCustomMode(true)
    setCustomName('')
    setCustomFilter('')
    setNewMaxRetry('5')
    setNewBanTime('600')
    setNewFindTime('600')
    setNewLogPath('')
    setNewIgnoreIP('')
  }, [])

  const handleCreateJail = useCallback(async () => {
    if (!selectedTemplate && !isCustomMode) return
    try {
      setAddJailLoading(true)
      if (isCustomMode) {
        await api.createFail2banJail({
          id: 'custom',
          name: customName,
          filter: customFilter,
          max_retry: parseInt(newMaxRetry, 10),
          ban_time: parseInt(newBanTime, 10),
          find_time: parseInt(newFindTime, 10),
          log_path: newLogPath,
          ignoreip: newIgnoreIP,
        })
      } else {
        await api.createFail2banJail({
          id: selectedTemplate!.id,
          max_retry: parseInt(newMaxRetry, 10),
          ban_time: parseInt(newBanTime, 10),
          find_time: parseInt(newFindTime, 10),
          log_path: newLogPath,
          ignoreip: newIgnoreIP,
        })
      }
      toast.success(t('firewall.fail2ban.jailCreated'))
      onOpenChange(false)
      onCreated()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('firewall.fail2ban.jailCreateFailed')
      toast.error(message)
    } finally {
      setAddJailLoading(false)
    }
  }, [selectedTemplate, isCustomMode, customName, customFilter, newMaxRetry, newBanTime, newFindTime, newLogPath, newIgnoreIP, t, onOpenChange, onCreated])

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!addJailLoading) onOpenChange(o) }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Plus className="h-4 w-4" />
            {t('firewall.fail2ban.addJailTitle')}
          </DialogTitle>
          <DialogDescription>
            {t('firewall.fail2ban.addJailDesc')}
          </DialogDescription>
        </DialogHeader>

        {/* Template Grid */}
        <div className="space-y-3">
          <label className="text-[13px] font-medium">{t('firewall.fail2ban.selectTemplate')}</label>
          {templatesLoading ? (
            <div className="flex items-center justify-center py-8 text-muted-foreground text-[13px]">
              <Loader2 className="h-4 w-4 animate-spin mr-2" />
              {t('common.loading')}
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-2 max-h-[240px] overflow-y-auto">
              {/* Custom jail option */}
              <button
                type="button"
                onClick={handleSelectCustom}
                className={`text-left rounded-xl px-3 py-2.5 border transition-all outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
                  isCustomMode
                    ? 'bg-primary/10 border-primary/30 ring-1 ring-primary/20'
                    : 'bg-card border-border hover:border-primary/30 hover:bg-muted/50'
                }`}
              >
                <div className="flex items-center justify-between">
                  <span className="text-[13px] font-medium">{t('firewall.fail2ban.customJail')}</span>
                  {isCustomMode && <Check className="h-3.5 w-3.5 text-primary" />}
                </div>
                <p className="text-[11px] text-muted-foreground mt-0.5 line-clamp-1">{t('firewall.fail2ban.customJailDesc')}</p>
              </button>
              {templates.map((tmpl) => {
                // Backend flips `available` off both when the jail is already
                // configured and when the filter file is missing — tell the two
                // apart by checking the active jail list.
                const isActive = !tmpl.available && activeJails.includes(tmpl.id)
                return (
                  <button
                    key={tmpl.id}
                    type="button"
                    onClick={() => tmpl.available && handleSelectTemplate(tmpl)}
                    disabled={!tmpl.available}
                    className={`text-left rounded-xl px-3 py-2.5 border transition-all outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
                      selectedTemplate?.id === tmpl.id
                        ? 'bg-primary/10 border-primary/30 ring-1 ring-primary/20'
                        : tmpl.available
                          ? 'bg-card border-border hover:border-primary/30 hover:bg-muted/50'
                          : 'bg-muted/30 border-border/50 opacity-60 cursor-not-allowed'
                    }`}
                  >
                    <div className="flex items-center justify-between">
                      <span className="text-[13px] font-medium font-mono">{tmpl.name}</span>
                      {selectedTemplate?.id === tmpl.id ? (
                        <Check className="h-3.5 w-3.5 text-primary" />
                      ) : !tmpl.available ? (
                        <span className="text-[10px] text-muted-foreground">
                          {isActive ? t('firewall.fail2ban.templateActive') : t('firewall.fail2ban.templateUnavailable')}
                        </span>
                      ) : (
                        <span className="text-[10px] text-success">{t('firewall.fail2ban.templateAvailable')}</span>
                      )}
                    </div>
                    <p className="text-[11px] text-muted-foreground mt-0.5 line-clamp-1">{tmpl.description}</p>
                  </button>
                )
              })}
            </div>
          )}
        </div>

        {/* Config fields (shown when template or custom selected) */}
        {(selectedTemplate || isCustomMode) && (
          <div className="space-y-3 border-t border-border/50 pt-4">
            {/* Custom-only fields: jail name and filter */}
            {isCustomMode && (
              <>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <label className="text-[13px] font-medium">{t('firewall.fail2ban.jailName')}</label>
                    <Input
                      value={customName}
                      onChange={(e) => setCustomName(e.target.value)}
                      placeholder="my-custom-jail"
                      className="rounded-xl text-[13px] font-mono"
                    />
                    <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.jailNameHint')}</p>
                  </div>
                  <div className="space-y-1.5">
                    <label className="text-[13px] font-medium">{t('firewall.fail2ban.filterName')}</label>
                    <Input
                      value={customFilter}
                      onChange={(e) => setCustomFilter(e.target.value)}
                      placeholder="sshd"
                      className="rounded-xl text-[13px] font-mono"
                    />
                    <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.filterNameHint')}</p>
                  </div>
                </div>
              </>
            )}
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.fail2ban.logPath')}</label>
              <Input
                value={newLogPath}
                onChange={(e) => setNewLogPath(e.target.value)}
                placeholder="/var/log/auth.log"
                className="rounded-xl text-[13px] font-mono"
              />
              <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.logPathHint')}</p>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <div className="space-y-1.5">
                <label className="text-[13px] font-medium">{t('firewall.fail2ban.maxRetry')}</label>
                <Input
                  type="number"
                  min={1}
                  value={newMaxRetry}
                  onChange={(e) => setNewMaxRetry(e.target.value)}
                  className="rounded-xl text-[13px] font-mono"
                />
              </div>
              <div className="space-y-1.5">
                <label className="text-[13px] font-medium">{t('firewall.fail2ban.banTime')}</label>
                <Input
                  type="number"
                  value={newBanTime}
                  onChange={(e) => setNewBanTime(e.target.value)}
                  className="rounded-xl text-[13px] font-mono"
                />
              </div>
              <div className="space-y-1.5">
                <label className="text-[13px] font-medium">{t('firewall.fail2ban.findTime')}</label>
                <Input
                  type="number"
                  value={newFindTime}
                  onChange={(e) => setNewFindTime(e.target.value)}
                  className="rounded-xl text-[13px] font-mono"
                />
              </div>
            </div>
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.fail2ban.ignoreIp')}</label>
              <Input
                placeholder="127.0.0.1/8 ::1"
                value={newIgnoreIP}
                onChange={(e) => setNewIgnoreIP(e.target.value)}
                className="rounded-xl text-[13px] font-mono"
              />
              <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.ignoreIpHelp')}</p>
            </div>
          </div>
        )}

        <DialogFooter className="gap-2">
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={addJailLoading}
            className="rounded-xl"
          >
            {t('common.cancel')}
          </Button>
          <Button
            onClick={handleCreateJail}
            disabled={addJailLoading || (!selectedTemplate && !isCustomMode) || (isCustomMode && (!customName || !customFilter || !newLogPath))}
            className="rounded-xl"
          >
            {addJailLoading ? (
              <>
                <Loader2 className="h-4 w-4 animate-spin" />
                {t('common.creating')}
              </>
            ) : (
              <>
                <Plus className="h-4 w-4" />
                {t('common.create')}
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
