import { useState, useEffect, useCallback, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { ListLoadState } from '@/components/ListLoadState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Tuning from '@/pages/settings/Tuning'
import { saveSetting } from '@/lib/saveSetting'

export default function Performance() {
  const { t } = useTranslation()

  const [terminalTimeout, setTerminalTimeout] = useState('30')
  const [terminalTimeoutLoading, setTerminalTimeoutLoading] = useState(false)

  const [maxUploadSize, setMaxUploadSize] = useState('1024')
  const [maxUploadSizeLoading, setMaxUploadSizeLoading] = useState(false)

  // The fields hold the defaults until the server's values arrive, and a read
  // that failed used to leave them there looking current: saving then wrote
  // 30 minutes / 1024 MB over whatever the node had. Until the values are in,
  // the forms are shut, and a failure says so with a way to try again.
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  // A value refused before saving, shown under its field (noValidate on the
  // forms: the browser's bubble came first, in the browser's language).
  const [fieldError, setFieldError] = useState<{ field: 'terminal-timeout' | 'max-upload-size'; message: string } | null>(null)

  const load = useCallback(() => {
    api.getSettings()
      .then((data) => {
        if (data.terminal_timeout !== undefined) setTerminalTimeout(data.terminal_timeout)
        if (data.max_upload_size !== undefined) setMaxUploadSize(data.max_upload_size)
        setLoaded(true)
      })
      .catch((err: unknown) => setLoadError(err instanceof Error ? err.message : String(err)))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  function refuse(field: 'terminal-timeout' | 'max-upload-size', message: string) {
    setFieldError({ field, message })
    document.getElementById(field)?.focus()
  }

  async function handleSaveTerminalTimeout(e: FormEvent) {
    e.preventDefault()
    const val = parseInt(terminalTimeout, 10)
    if (isNaN(val) || val < 0) {
      refuse('terminal-timeout', t('settings.invalidTimeout'))
      return
    }
    setFieldError(null)
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
      refuse('max-upload-size', t('settings.invalidMaxUploadSize'))
      return
    }
    setFieldError(null)
    setMaxUploadSizeLoading(true)
    await saveSetting('max_upload_size', String(val), {
      success: t('settings.settingsSaved'),
      failure: t('settings.settingsSaveFailed'),
    })
    setMaxUploadSizeLoading(false)
  }

  return (
    <div className="space-y-6 mt-6">
      {loadError && (
        <ListLoadState loading={false} error={loadError} errorTitle={t('settings.settingsLoadFailed')} onRetry={() => { setLoadError(null); load() }} />
      )}
      {/* Terminal — per-node (settings table is local SQLite, not FSM) */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.terminal')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.terminalDescription')}</p>
        <form onSubmit={handleSaveTerminalTimeout} noValidate className="space-y-4 max-w-md">
          <div className="space-y-2">
            <Label htmlFor="terminal-timeout" className="text-[13px]">{t('settings.terminalTimeout')}</Label>
            <div className="flex items-center gap-2">
              <Input
                id="terminal-timeout"
                type="number"
                min="0"
                value={terminalTimeout}
                onChange={(e) => setTerminalTimeout(e.target.value)}
                disabled={!loaded}
                aria-invalid={fieldError?.field === 'terminal-timeout' || undefined}
                aria-describedby={fieldError?.field === 'terminal-timeout' ? 'terminal-timeout-hint terminal-timeout-error' : 'terminal-timeout-hint'}
                className="w-24 rounded-xl"
              />
              <span className="text-[13px] text-muted-foreground">{t('settings.minutes')}</span>
            </div>
            <p id="terminal-timeout-hint" className="text-[12px] text-muted-foreground">{t('settings.terminalTimeoutHint')}</p>
            {fieldError?.field === 'terminal-timeout' && <p id="terminal-timeout-error" className="text-[12px] text-destructive">{fieldError.message}</p>}
          </div>
          <Button type="submit" disabled={!loaded || terminalTimeoutLoading} className="rounded-xl">
            {terminalTimeoutLoading ? t('common.saving') : t('common.save')}
          </Button>
        </form>
      </div>

      {/* File Upload — per-node */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.fileUpload')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.fileUploadDescription')}</p>
        <form onSubmit={handleSaveMaxUploadSize} noValidate className="space-y-4 max-w-md">
          <div className="space-y-2">
            <Label htmlFor="max-upload-size" className="text-[13px]">{t('settings.maxUploadSize')}</Label>
            <div className="flex items-center gap-2">
              <Input
                id="max-upload-size"
                type="number"
                min="1"
                value={maxUploadSize}
                onChange={(e) => setMaxUploadSize(e.target.value)}
                disabled={!loaded}
                aria-invalid={fieldError?.field === 'max-upload-size' || undefined}
                aria-describedby={fieldError?.field === 'max-upload-size' ? 'max-upload-size-hint max-upload-size-error' : 'max-upload-size-hint'}
                className="w-24 rounded-xl"
              />
              <span className="text-[13px] text-muted-foreground">MB</span>
            </div>
            <p id="max-upload-size-hint" className="text-[12px] text-muted-foreground">{t('settings.maxUploadSizeHint')}</p>
            {fieldError?.field === 'max-upload-size' && <p id="max-upload-size-error" className="text-[12px] text-destructive">{fieldError.message}</p>}
          </div>
          <Button type="submit" disabled={!loaded || maxUploadSizeLoading} className="rounded-xl">
            {maxUploadSizeLoading ? t('common.saving') : t('common.save')}
          </Button>
        </form>
      </div>

      <Tuning />
    </div>
  )
}
