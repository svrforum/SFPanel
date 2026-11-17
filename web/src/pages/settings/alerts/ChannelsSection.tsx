import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { ListLoadState } from '@/components/ListLoadState'
import { StatusPill } from '@/components/StatusPill'
import { useApiAction } from '@/hooks/useApiAction'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Trash2, Plus, Send, X } from 'lucide-react'
import type { AlertChannel } from '@/types/api'

type ChannelType = 'discord' | 'telegram' | 'webhook'

const EMPTY_FORM = { name: '', type: 'discord' as ChannelType, webhook_url: '', bot_token: '', chat_id: '' }

type ChannelField = 'name' | 'type' | 'webhook_url' | 'bot_token' | 'chat_id'

export function ChannelsSection({ channels, loaded, loadError, onRetry, onChanged }: {
  channels: AlertChannel[]
  loaded: boolean
  loadError: string | null
  onRetry: () => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [showAddChannel, setShowAddChannel] = useState(false)
  const [channelForm, setChannelForm] = useState(EMPTY_FORM)
  const [testingId, setTestingId] = useState<number | null>(null)
  // Each label is tied to its field, and a field the form refuses says so
  // under itself (it was a toast that left the field unmarked).
  const formId = useId()
  const fieldId = (field: ChannelField) => `${formId}-${field}`
  const [formError, setFormError] = useState<{ field: ChannelField; message: string } | null>(null)
  const invalid = (field: ChannelField) => ({
    'aria-invalid': formError?.field === field || undefined,
    'aria-describedby': formError?.field === field ? `${fieldId(field)}-error` : undefined,
  })
  const fieldError = (field: ChannelField) => formError?.field === field && (
    <p id={`${fieldId(field)}-error`} className="text-[12px] text-destructive">{formError.message}</p>
  )

  const { run: runAddChannel, loading: channelLoading } = useApiAction(
    api.createAlertChannel.bind(api),
    {
      successMsg: t('settings.alerts.channels.successAdded'),
      errorMsg: t('settings.alerts.channels.errorAddFailed'),
      onSuccess: () => {
        setShowAddChannel(false)
        setChannelForm(EMPTY_FORM)
        setFormError(null)
        onChanged()
      },
    },
  )

  const { run: runToggleChannel } = useApiAction(
    api.updateAlertChannel.bind(api),
    { errorMsg: t('settings.alerts.channels.errorToggleFailed'), onSuccess: onChanged },
  )

  const { run: runDeleteChannel } = useApiAction(
    api.deleteAlertChannel.bind(api),
    {
      successMsg: t('settings.alerts.channels.successDeleted'),
      errorMsg: t('settings.alerts.channels.errorDeleteFailed'),
      onSuccess: onChanged,
    },
  )

  const { run: runTestChannel } = useApiAction(
    api.testAlertChannel.bind(api),
    {
      successMsg: t('settings.alerts.channels.successTested'),
      errorMsg: t('settings.alerts.channels.errorTestFailed'),
    },
  )

  function refuse(field: ChannelField, message: string) {
    setFormError({ field, message })
    document.getElementById(fieldId(field))?.focus()
  }

  async function handleAddChannel() {
    if (!channelForm.name.trim()) {
      refuse('name', t('settings.alerts.channels.errorNameRequired'))
      return
    }
    const config: Record<string, string> = {}
    if (channelForm.type === 'discord' || channelForm.type === 'webhook') {
      if (!channelForm.webhook_url.trim()) { refuse('webhook_url', t('settings.alerts.channels.errorWebhookRequired')); return }
      config.webhook_url = channelForm.webhook_url
    } else {
      if (!channelForm.bot_token.trim() || !channelForm.chat_id.trim()) {
        refuse(channelForm.bot_token.trim() ? 'chat_id' : 'bot_token', t('settings.alerts.channels.errorTelegramRequired')); return
      }
      config.bot_token = channelForm.bot_token
      config.chat_id = channelForm.chat_id
    }
    setFormError(null)
    await runAddChannel({ name: channelForm.name, type: channelForm.type, config, enabled: true })
  }

  async function handleToggleChannel(ch: AlertChannel) {
    // Send only `enabled` — UpdateChannel uses NULLIF/COALESCE so empty
    // fields preserve existing DB values. Round-tripping `ch.config`
    // would overwrite Discord webhooks / Telegram bot tokens with the
    // masked values returned by ListChannels (`***xxxx`).
    await runToggleChannel(ch.id, { enabled: !ch.enabled })
  }

  async function handleDeleteChannel(ch: AlertChannel) {
    if (!(await confirm({
      title: t('settings.alerts.channels.confirmDelete', { name: ch.name }),
      description: t('settings.alerts.channels.confirmDeleteDesc'),
      confirmLabel: t('settings.alerts.channels.deleteAction'),
      danger: true,
    }))) return
    await runDeleteChannel(ch.id)
  }

  async function handleTestChannel(id: number) {
    setTestingId(id)
    await runTestChannel(id) // run never throws — failures surface as toasts
    setTestingId(null)
  }

  return (
    <div className="bg-card rounded-2xl p-6 card-shadow">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-[15px] font-semibold">{t('settings.alerts.channels.title')}</h2>
          <p className="text-[13px] text-muted-foreground mt-1">{t('settings.alerts.channels.description')}</p>
        </div>
        <Button
          variant="outline"
          size="sm"
          className="rounded-xl text-[13px]"
          onClick={() => setShowAddChannel(!showAddChannel)}
        >
          {showAddChannel ? <X className="h-3.5 w-3.5 mr-1.5" /> : <Plus className="h-3.5 w-3.5 mr-1.5" />}
          {showAddChannel ? t('common.cancel') : t('settings.alerts.channels.addButton')}
        </Button>
      </div>

      {/* Add channel form */}
      {showAddChannel && (
        <div className="bg-secondary/30 rounded-xl p-4 mb-4 space-y-3">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor={fieldId('name')} className="text-[13px]">{t('settings.alerts.channels.formNameLabel')}</Label>
              <Input
                id={fieldId('name')}
                value={channelForm.name}
                onChange={e => setChannelForm(f => ({ ...f, name: e.target.value }))}
                placeholder={t('settings.alerts.channels.formNamePlaceholder')}
                {...invalid('name')}
                className="rounded-xl"
              />
              {fieldError('name')}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor={fieldId('type')} className="text-[13px]">{t('settings.alerts.channels.formTypeLabel')}</Label>
              <Select value={channelForm.type} onValueChange={v => setChannelForm(f => ({ ...f, type: v as ChannelType }))}>
                <SelectTrigger id={fieldId('type')} className="rounded-xl w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="discord">Discord</SelectItem>
                  <SelectItem value="telegram">Telegram</SelectItem>
                  <SelectItem value="webhook">Webhook</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          {channelForm.type === 'discord' || channelForm.type === 'webhook' ? (
            <div className="space-y-1.5">
              <Label htmlFor={fieldId('webhook_url')} className="text-[13px]">
                {channelForm.type === 'webhook'
                  ? t('settings.alerts.channels.formWebhookUrlLabel')
                  : t('settings.alerts.channels.formWebhookLabel')}
              </Label>
              <Input
                id={fieldId('webhook_url')}
                type="url"
                value={channelForm.webhook_url}
                onChange={e => setChannelForm(f => ({ ...f, webhook_url: e.target.value }))}
                placeholder={channelForm.type === 'webhook'
                  ? t('settings.alerts.channels.formWebhookUrlPlaceholder')
                  : t('settings.alerts.channels.formWebhookPlaceholder')}
                {...invalid('webhook_url')}
                className="rounded-xl"
              />
              {fieldError('webhook_url')}
              {channelForm.type === 'webhook' && (
                <p className="text-[12px] text-muted-foreground">{t('settings.alerts.channels.formWebhookHint')}</p>
              )}
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor={fieldId('bot_token')} className="text-[13px]">{t('settings.alerts.channels.formBotTokenLabel')}</Label>
                <Input
                  id={fieldId('bot_token')}
                  value={channelForm.bot_token}
                  onChange={e => setChannelForm(f => ({ ...f, bot_token: e.target.value }))}
                  placeholder={t('settings.alerts.channels.formBotTokenPlaceholder')}
                  autoComplete="off"
                  spellCheck={false}
                  {...invalid('bot_token')}
                  className="rounded-xl"
                />
                {fieldError('bot_token')}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor={fieldId('chat_id')} className="text-[13px]">{t('settings.alerts.channels.formChatIdLabel')}</Label>
                <Input
                  id={fieldId('chat_id')}
                  value={channelForm.chat_id}
                  onChange={e => setChannelForm(f => ({ ...f, chat_id: e.target.value }))}
                  placeholder={t('settings.alerts.channels.formChatIdPlaceholder')}
                  {...invalid('chat_id')}
                  className="rounded-xl"
                />
                {fieldError('chat_id')}
              </div>
            </div>
          )}
          <Button onClick={handleAddChannel} disabled={channelLoading} className="rounded-xl">
            {channelLoading ? t('settings.alerts.channels.addInProgress') : t('settings.alerts.channels.addButton')}
          </Button>
        </div>
      )}

      {/* Channel list: "none" only once the list has been read. */}
      {(loadError || !loaded) && (
        <ListLoadState loading={!loaded} error={loadError} errorTitle={t('settings.alerts.channels.loadFailed')} onRetry={onRetry} skeletonCount={2} />
      )}
      {!loaded ? null : channels.length === 0 ? (
        <p className="text-[13px] text-muted-foreground py-4">{t('settings.alerts.channels.empty')}</p>
      ) : (
        <div className="space-y-2">
          {channels.map(ch => (
            // Wraps on a phone: with no wrap the buttons ran past the clipped
            // edge of the page, the delete button out of reach.
            <div key={ch.id} className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 bg-secondary/30 rounded-xl px-4 py-3">
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                {/* A neutral pill: brand colours on their own 10% tint were
                    3.3–4.0:1. The type is in the word. */}
                <StatusPill tone="secondary">
                  {ch.type === 'discord' ? 'Discord' : ch.type === 'telegram' ? 'Telegram' : 'Webhook'}
                </StatusPill>
                <span className="text-[13px] font-medium break-all">{ch.name}</span>
                <span className={`inline-flex items-center px-1.5 py-0.5 rounded-full text-[12px] font-medium ${
                  ch.enabled ? 'bg-success/10 text-success' : 'bg-secondary text-muted-foreground'
                }`}>
                  {ch.enabled ? t('common.active') : t('common.disabled')}
                </span>
              </div>
              <div className="flex items-center gap-1.5 ms-auto">
                <Button
                  variant="outline"
                  size="sm"
                  className="rounded-xl h-7 px-2 text-[12px]"
                  onClick={() => handleToggleChannel(ch)}
                >
                  {ch.enabled ? t('settings.alerts.channels.actionDisable') : t('settings.alerts.channels.actionEnable')}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="rounded-xl h-7 px-2 text-[12px]"
                  onClick={() => handleTestChannel(ch.id)}
                  disabled={testingId === ch.id}
                >
                  <Send className="h-3 w-3 mr-1" />
                  {testingId === ch.id ? t('settings.alerts.channels.actionTesting') : t('settings.alerts.channels.actionTest')}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="rounded-xl h-7 px-2 text-destructive hover:text-destructive"
                  onClick={() => handleDeleteChannel(ch)}
                  aria-label={t('common.delete')}
                >
                  <Trash2 className="h-3 w-3" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
