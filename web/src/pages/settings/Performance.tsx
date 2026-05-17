import { useState, useEffect, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import SettingsTuning from '@/pages/SettingsTuning'
import { saveSetting } from '@/lib/saveSetting'

export default function Performance() {
  const { t } = useTranslation()

  const [terminalTimeout, setTerminalTimeout] = useState('30')
  const [terminalTimeoutLoading, setTerminalTimeoutLoading] = useState(false)

  const [maxUploadSize, setMaxUploadSize] = useState('1024')
  const [maxUploadSizeLoading, setMaxUploadSizeLoading] = useState(false)

  useEffect(() => {
    api.getSettings()
      .then((data) => {
        if (data.terminal_timeout !== undefined) setTerminalTimeout(data.terminal_timeout)
        if (data.max_upload_size !== undefined) setMaxUploadSize(data.max_upload_size)
      })
      .catch(() => { /* ignore */ })
  }, [])

  async function handleSaveTerminalTimeout(e: FormEvent) {
    e.preventDefault()
    const val = parseInt(terminalTimeout, 10)
    if (isNaN(val) || val < 0) {
      toast.error(t('settings.invalidTimeout'))
      return
    }
    setTerminalTimeoutLoading(true)
    await saveSetting('terminal_timeout', String(val), {
      success: t('settings.settingsSaved'),
      failure: t('settings.settingsSaveFailed'),
    })
    setTerminalTimeoutLoading(false)
  }

  async function handleSaveMaxUploadSize(e: FormEvent) {
    e.preventDefault()
    const val = parseInt(maxUploadSize, 10)
    if (isNaN(val) || val < 1) {
      toast.error(t('settings.invalidMaxUploadSize'))
      return
    }
    setMaxUploadSizeLoading(true)
    await saveSetting('max_upload_size', String(val), {
      success: t('settings.settingsSaved'),
      failure: t('settings.settingsSaveFailed'),
    })
    setMaxUploadSizeLoading(false)
  }

  return (
    <div className="space-y-6 mt-6">
      {/* Terminal — per-node (settings table is local SQLite, not FSM) */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.terminal')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.terminalDescription')}</p>
        <form onSubmit={handleSaveTerminalTimeout} className="space-y-4 max-w-md">
          <div className="space-y-2">
            <Label htmlFor="terminal-timeout" className="text-[13px]">{t('settings.terminalTimeout')}</Label>
            <div className="flex items-center gap-2">
              <Input
                id="terminal-timeout"
                type="number"
                min="0"
                value={terminalTimeout}
                onChange={(e) => setTerminalTimeout(e.target.value)}
                className="w-24 rounded-xl"
              />
              <span className="text-[13px] text-muted-foreground">{t('settings.minutes')}</span>
            </div>
            <p className="text-[11px] text-muted-foreground">{t('settings.terminalTimeoutHint')}</p>
          </div>
          <Button type="submit" disabled={terminalTimeoutLoading} className="rounded-xl">
            {terminalTimeoutLoading ? t('common.saving') : t('common.save')}
          </Button>
        </form>
      </div>

      {/* File Upload — per-node */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.fileUpload')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.fileUploadDescription')}</p>
        <form onSubmit={handleSaveMaxUploadSize} className="space-y-4 max-w-md">
          <div className="space-y-2">
            <Label htmlFor="max-upload-size" className="text-[13px]">{t('settings.maxUploadSize')}</Label>
            <div className="flex items-center gap-2">
              <Input
                id="max-upload-size"
                type="number"
                min="1"
                value={maxUploadSize}
                onChange={(e) => setMaxUploadSize(e.target.value)}
                className="w-24 rounded-xl"
              />
              <span className="text-[13px] text-muted-foreground">MB</span>
            </div>
            <p className="text-[11px] text-muted-foreground">{t('settings.maxUploadSizeHint')}</p>
          </div>
          <Button type="submit" disabled={maxUploadSizeLoading} className="rounded-xl">
            {maxUploadSizeLoading ? t('common.saving') : t('common.save')}
          </Button>
        </form>
      </div>

      <SettingsTuning />
    </div>
  )
}
