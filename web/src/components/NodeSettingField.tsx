import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { saveSetting } from '@/lib/saveSetting'
import { parseSettingNumber } from '@/lib/settingValue'
import { ListLoadState } from '@/components/ListLoadState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export interface NodeSettingFieldProps {
  settingKey: 'terminal_timeout' | 'max_upload_size'
  id: string
  label: string
  unit: string
  hint: string
  min: number
  invalid: string
}

/**
 * One of a server's own settings, read when the field mounts and saved on its
 * own. Both calls follow ?node=: the settings table is each server's, and
 * nothing replicates it.
 *
 * Until the value is in, the field and its button are shut: a field showing a
 * default after a failed read looked current, and saving it wrote the default
 * over the server's value. A failed read says so, with a way to try again. A
 * value refused before saving is said beside the field (noValidate: the
 * browser's bubble came first, in the browser's language) and sends nothing.
 */
export function NodeSettingField({ settingKey, id, label, unit, hint, min, invalid }: NodeSettingFieldProps) {
  const { t } = useTranslation()
  const inputRef = useRef<HTMLInputElement>(null)
  const [value, setValue] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [refused, setRefused] = useState(false)
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    api.getSettings()
      .then((data) => {
        setValue(data[settingKey] ?? '')
        setLoaded(true)
      })
      .catch((err: unknown) => setLoadError(err instanceof Error ? err.message : String(err)))
  }, [settingKey])

  useEffect(() => {
    load()
  }, [load])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    const parsed = parseSettingNumber(value, min)
    if (parsed === null) {
      setRefused(true)
      inputRef.current?.focus()
      return
    }
    setRefused(false)
    setSaving(true)
    await saveSetting(settingKey, String(parsed), {
      success: t('settings.settingsSaved'),
      failure: t('settings.settingsSaveFailed'),
    })
    setSaving(false)
  }

  return (
    <div className="space-y-2">
      {/* Outside the form: its Retry is a button, and in the form it would submit. */}
      {loadError && (
        <ListLoadState loading={false} error={loadError} errorTitle={t('settings.settingsLoadFailed')} onRetry={() => { setLoadError(null); load() }} />
      )}
      <form onSubmit={handleSubmit} noValidate className="space-y-2">
        <Label htmlFor={id} className="text-[13px]">{label}</Label>
        <div className="flex items-center gap-2">
          <Input
            ref={inputRef}
            id={id}
            type="number"
            min={min}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            disabled={!loaded}
            aria-invalid={refused || undefined}
            aria-describedby={refused ? `${id}-hint ${id}-error` : `${id}-hint`}
            className="w-24 rounded-xl"
          />
          <span className="text-[13px] text-muted-foreground">{unit}</span>
          <Button type="submit" size="sm" disabled={!loaded || saving} className="rounded-xl">
            {saving ? t('common.saving') : t('common.save')}
          </Button>
        </div>
        <p id={`${id}-hint`} className="text-[12px] text-muted-foreground">{hint}</p>
        {refused && <p id={`${id}-error`} className="text-[12px] text-destructive">{invalid}</p>}
      </form>
    </div>
  )
}
