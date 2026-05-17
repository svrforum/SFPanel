import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Checkbox } from '@/components/ui/checkbox'
import { Trash2, Plus, Send, ChevronLeft, ChevronRight, X } from 'lucide-react'

// Types
interface AlertChannel {
  id: number
  name: string
  type: 'discord' | 'telegram'
  config: Record<string, string>
  enabled: boolean
  created_at: string
}

interface AlertRule {
  id: number
  name: string
  type: string
  threshold: number
  severity: 'info' | 'warning' | 'critical'
  cooldown: number
  channels: number[]
  node_scope: string
  nodes: string[]
  enabled: boolean
  created_at: string
}

interface AlertHistoryEntry {
  id: number
  type: string
  severity: string
  message: string
  node: string
  status: string
  created_at: string
}

// Rule type value <-> i18n key mapping. Labels are resolved via t() at render time.
const RULE_TYPES: { value: string; i18nKey: string }[] = [
  { value: 'cpu', i18nKey: 'settings.alerts.ruleType.cpu' },
  { value: 'memory', i18nKey: 'settings.alerts.ruleType.memory' },
  { value: 'disk', i18nKey: 'settings.alerts.ruleType.disk' },
  { value: 'container_down', i18nKey: 'settings.alerts.ruleType.containerDown' },
  { value: 'container_oom', i18nKey: 'settings.alerts.ruleType.containerOom' },
  { value: 'container_restart_loop', i18nKey: 'settings.alerts.ruleType.containerRestartLoop' },
  { value: 'container_unhealthy', i18nKey: 'settings.alerts.ruleType.containerUnhealthy' },
  { value: 'service', i18nKey: 'settings.alerts.ruleType.service' },
  { value: 'login', i18nKey: 'settings.alerts.ruleType.login' },
  { value: 'package', i18nKey: 'settings.alerts.ruleType.package' },
]

// Rule types that operate on containers (not host metrics) and use a JSON
// `condition` payload instead of the simple `threshold` percentage.
const CONTAINER_RULE_TYPES = new Set(['container_down', 'container_oom', 'container_restart_loop', 'container_unhealthy'])

const SEVERITY_OPTIONS = [
  { value: 'info', label: 'Info', color: 'bg-[#3182f6]/10 text-[#3182f6]' },
  { value: 'warning', label: 'Warning', color: 'bg-[#f59e0b]/10 text-[#f59e0b]' },
  { value: 'critical', label: 'Critical', color: 'bg-[#f04452]/10 text-[#f04452]' },
]

function getSeverityStyle(severity: string) {
  return SEVERITY_OPTIONS.find(s => s.value === severity)?.color || 'bg-secondary text-muted-foreground'
}

export default function AlertSettings() {
  const { t } = useTranslation()

  // Channel state
  const [channels, setChannels] = useState<AlertChannel[]>([])
  const [showAddChannel, setShowAddChannel] = useState(false)
  const [channelForm, setChannelForm] = useState({
    name: '', type: 'discord' as 'discord' | 'telegram',
    webhook_url: '', bot_token: '', chat_id: '',
  })
  const [channelLoading, setChannelLoading] = useState(false)
  const [testingId, setTestingId] = useState<number | null>(null)

  // Rule state
  const [rules, setRules] = useState<AlertRule[]>([])
  const [showAddRule, setShowAddRule] = useState(false)
  const [ruleForm, setRuleForm] = useState({
    name: '', type: 'cpu', threshold: 90, severity: 'warning' as 'info' | 'warning' | 'critical',
    cooldown: 300, channels: [] as number[], node_scope: 'all', nodes: [] as string[],
  })
  // Container-rule extra inputs (only used when ruleForm.type is a CONTAINER_RULE_TYPES member)
  const [containerPattern, setContainerPattern] = useState('*')
  const [thresholdCount, setThresholdCount] = useState(3)
  const [windowSeconds, setWindowSeconds] = useState(300)
  const [ruleLoading, setRuleLoading] = useState(false)

  // History state
  const [history, setHistory] = useState<AlertHistoryEntry[]>([])
  const [historyTotal, setHistoryTotal] = useState(0)
  const [historyPage, setHistoryPage] = useState(1)
  const historyLimit = 20

  // Load data
  const loadChannels = useCallback(async () => {
    try {
      const data = await api.request<AlertChannel[]>('/alerts/channels')
      setChannels(data)
    } catch { /* ignore */ }
  }, [])

  const loadRules = useCallback(async () => {
    try {
      const data = await api.request<AlertRule[]>('/alerts/rules')
      setRules(data)
    } catch { /* ignore */ }
  }, [])

  const loadHistory = useCallback(async (page: number) => {
    try {
      const data = await api.request<{ items: AlertHistoryEntry[]; total: number }>(
        `/alerts/history?page=${page}&limit=${historyLimit}`
      )
      setHistory(data.items || [])
      setHistoryTotal(data.total || 0)
    } catch { /* ignore */ }
  }, [])

  useEffect(() => {
    loadChannels()
    loadRules()
    loadHistory(1)
  }, [loadChannels, loadRules, loadHistory])

  // Channel handlers
  async function handleAddChannel() {
    if (!channelForm.name.trim()) {
      toast.error(t('settings.alerts.channels.errorNameRequired'))
      return
    }
    const config: Record<string, string> = {}
    if (channelForm.type === 'discord') {
      if (!channelForm.webhook_url.trim()) { toast.error(t('settings.alerts.channels.errorWebhookRequired')); return }
      config.webhook_url = channelForm.webhook_url
    } else {
      if (!channelForm.bot_token.trim() || !channelForm.chat_id.trim()) {
        toast.error(t('settings.alerts.channels.errorTelegramRequired')); return
      }
      config.bot_token = channelForm.bot_token
      config.chat_id = channelForm.chat_id
    }
    setChannelLoading(true)
    try {
      await api.request('/alerts/channels', {
        method: 'POST',
        body: JSON.stringify({ name: channelForm.name, type: channelForm.type, config, enabled: true }),
      })
      toast.success(t('settings.alerts.channels.successAdded'))
      setShowAddChannel(false)
      setChannelForm({ name: '', type: 'discord', webhook_url: '', bot_token: '', chat_id: '' })
      loadChannels()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('settings.alerts.channels.errorAddFailed'))
    } finally {
      setChannelLoading(false)
    }
  }

  async function handleToggleChannel(ch: AlertChannel) {
    try {
      // Send only `enabled` — UpdateChannel uses NULLIF/COALESCE so empty
      // fields preserve existing DB values. Round-tripping `ch.config`
      // would overwrite Discord webhooks / Telegram bot tokens with the
      // masked values returned by ListChannels (`***xxxx`).
      await api.request(`/alerts/channels/${ch.id}`, {
        method: 'PUT',
        body: JSON.stringify({ enabled: !ch.enabled }),
      })
      loadChannels()
    } catch { /* ignore */ }
  }

  async function handleDeleteChannel(id: number) {
    if (!window.confirm(t('settings.alerts.channels.confirmDelete'))) return
    try {
      await api.request(`/alerts/channels/${id}`, { method: 'DELETE' })
      toast.success(t('settings.alerts.channels.successDeleted'))
      loadChannels()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('settings.alerts.channels.errorDeleteFailed'))
    }
  }

  async function handleTestChannel(id: number) {
    setTestingId(id)
    try {
      await api.request(`/alerts/channels/${id}/test`, { method: 'POST' })
      toast.success(t('settings.alerts.channels.successTested'))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('settings.alerts.channels.errorTestFailed'))
    } finally {
      setTestingId(null)
    }
  }

  // Build the JSON `condition` payload the backend expects per rule type.
  // Host metric rules (cpu/memory/disk) use {operator,threshold}; container
  // rules use a pattern + optional restart-loop window. Returns a JSON string.
  function buildConditionForSubmit(type: string): string {
    if (type === 'container_down' || type === 'container_oom' || type === 'container_unhealthy') {
      return JSON.stringify({ container_pattern: containerPattern || '*' })
    }
    if (type === 'container_restart_loop') {
      return JSON.stringify({
        container_pattern: containerPattern || '*',
        threshold_count: thresholdCount || 3,
        window_seconds: windowSeconds || 300,
      })
    }
    // Host types: cpu/memory/disk — server-side evaluator reads operator+threshold.
    return JSON.stringify({ operator: '>', threshold: ruleForm.threshold })
  }

  // Rule handlers
  async function handleAddRule() {
    if (!ruleForm.name.trim()) { toast.error(t('settings.alerts.rules.errorNameRequired')); return }
    if (ruleForm.channels.length === 0) { toast.error(t('settings.alerts.rules.errorChannelsRequired')); return }
    setRuleLoading(true)
    try {
      await api.request('/alerts/rules', {
        method: 'POST',
        body: JSON.stringify({
          name: ruleForm.name,
          type: ruleForm.type,
          condition: buildConditionForSubmit(ruleForm.type),
          channel_ids: JSON.stringify(ruleForm.channels),
          severity: ruleForm.severity,
          cooldown: ruleForm.cooldown,
          node_scope: ruleForm.node_scope,
          node_ids: JSON.stringify(ruleForm.nodes),
          enabled: true,
        }),
      })
      toast.success(t('settings.alerts.rules.successAdded'))
      setShowAddRule(false)
      setRuleForm({ name: '', type: 'cpu', threshold: 90, severity: 'warning', cooldown: 300, channels: [], node_scope: 'all', nodes: [] })
      setContainerPattern('*')
      setThresholdCount(3)
      setWindowSeconds(300)
      loadRules()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('settings.alerts.rules.errorAddFailed'))
    } finally {
      setRuleLoading(false)
    }
  }

  async function handleToggleRule(rule: AlertRule) {
    try {
      await api.request(`/alerts/rules/${rule.id}`, {
        method: 'PUT',
        body: JSON.stringify({ enabled: !rule.enabled }),
      })
      loadRules()
    } catch { /* ignore */ }
  }

  async function handleDeleteRule(id: number) {
    if (!window.confirm(t('settings.alerts.rules.confirmDelete'))) return
    try {
      await api.request(`/alerts/rules/${id}`, { method: 'DELETE' })
      toast.success(t('settings.alerts.rules.successDeleted'))
      loadRules()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('settings.alerts.rules.errorDeleteFailed'))
    }
  }

  // History handlers
  async function handleClearHistory() {
    if (!window.confirm(t('settings.alerts.history.confirmClear'))) return
    try {
      await api.request('/alerts/history', { method: 'DELETE' })
      toast.success(t('settings.alerts.history.successCleared'))
      setHistory([])
      setHistoryTotal(0)
      setHistoryPage(1)
    } catch { /* ignore */ }
  }

  function toggleRuleChannel(channelId: number) {
    setRuleForm(prev => ({
      ...prev,
      channels: prev.channels.includes(channelId)
        ? prev.channels.filter(id => id !== channelId)
        : [...prev.channels, channelId],
    }))
  }

  return (
    <div className="space-y-6">

      {/* ===== Section 1: 채널 관리 ===== */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-[15px] font-semibold">{t('settings.alerts.channels.title')}</h3>
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
                <Label className="text-[13px]">{t('settings.alerts.channels.formNameLabel')}</Label>
                <Input
                  value={channelForm.name}
                  onChange={e => setChannelForm(f => ({ ...f, name: e.target.value }))}
                  placeholder={t('settings.alerts.channels.formNamePlaceholder')}
                  className="rounded-xl"
                />
              </div>
              <div className="space-y-1.5">
                <Label className="text-[13px]">{t('settings.alerts.channels.formTypeLabel')}</Label>
                <Select value={channelForm.type} onValueChange={v => setChannelForm(f => ({ ...f, type: v as 'discord' | 'telegram' }))}>
                  <SelectTrigger className="rounded-xl w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="discord">Discord</SelectItem>
                    <SelectItem value="telegram">Telegram</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
            {channelForm.type === 'discord' ? (
              <div className="space-y-1.5">
                <Label className="text-[13px]">{t('settings.alerts.channels.formWebhookLabel')}</Label>
                <Input
                  value={channelForm.webhook_url}
                  onChange={e => setChannelForm(f => ({ ...f, webhook_url: e.target.value }))}
                  placeholder={t('settings.alerts.channels.formWebhookPlaceholder')}
                  className="rounded-xl"
                />
              </div>
            ) : (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label className="text-[13px]">{t('settings.alerts.channels.formBotTokenLabel')}</Label>
                  <Input
                    value={channelForm.bot_token}
                    onChange={e => setChannelForm(f => ({ ...f, bot_token: e.target.value }))}
                    placeholder={t('settings.alerts.channels.formBotTokenPlaceholder')}
                    className="rounded-xl"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label className="text-[13px]">{t('settings.alerts.channels.formChatIdLabel')}</Label>
                  <Input
                    value={channelForm.chat_id}
                    onChange={e => setChannelForm(f => ({ ...f, chat_id: e.target.value }))}
                    placeholder={t('settings.alerts.channels.formChatIdPlaceholder')}
                    className="rounded-xl"
                  />
                </div>
              </div>
            )}
            <Button onClick={handleAddChannel} disabled={channelLoading} className="rounded-xl">
              {channelLoading ? t('settings.alerts.channels.addInProgress') : t('settings.alerts.channels.addButton')}
            </Button>
          </div>
        )}

        {/* Channel list */}
        {channels.length === 0 ? (
          <p className="text-[13px] text-muted-foreground py-4">{t('settings.alerts.channels.empty')}</p>
        ) : (
          <div className="space-y-2">
            {channels.map(ch => (
              <div key={ch.id} className="flex items-center justify-between bg-secondary/30 rounded-xl px-4 py-3">
                <div className="flex items-center gap-3">
                  <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium ${
                    ch.type === 'discord' ? 'bg-[#5865F2]/10 text-[#5865F2]' : 'bg-[#0088cc]/10 text-[#0088cc]'
                  }`}>
                    {ch.type === 'discord' ? 'Discord' : 'Telegram'}
                  </span>
                  <span className="text-[13px] font-medium">{ch.name}</span>
                  <span className={`inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium ${
                    ch.enabled ? 'bg-[#00c471]/10 text-[#00c471]' : 'bg-secondary text-muted-foreground'
                  }`}>
                    {ch.enabled ? t('common.active') : t('common.disabled')}
                  </span>
                </div>
                <div className="flex items-center gap-1.5">
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[11px]"
                    onClick={() => handleToggleChannel(ch)}
                  >
                    {ch.enabled ? t('settings.alerts.channels.actionDisable') : t('settings.alerts.channels.actionEnable')}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[11px]"
                    onClick={() => handleTestChannel(ch.id)}
                    disabled={testingId === ch.id}
                  >
                    <Send className="h-3 w-3 mr-1" />
                    {testingId === ch.id ? t('settings.alerts.channels.actionTesting') : t('settings.alerts.channels.actionTest')}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[#f04452] hover:text-[#f04452]"
                    onClick={() => handleDeleteChannel(ch.id)}
                  >
                    <Trash2 className="h-3 w-3" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* ===== Section 2: 규칙 관리 ===== */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-[15px] font-semibold">{t('settings.alerts.rules.title')}</h3>
            <p className="text-[13px] text-muted-foreground mt-1">{t('settings.alerts.rules.description')}</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="rounded-xl text-[13px]"
            onClick={() => setShowAddRule(!showAddRule)}
          >
            {showAddRule ? <X className="h-3.5 w-3.5 mr-1.5" /> : <Plus className="h-3.5 w-3.5 mr-1.5" />}
            {showAddRule ? t('common.cancel') : t('settings.alerts.rules.addButton')}
          </Button>
        </div>

        {/* Add rule form */}
        {showAddRule && (
          <div className="bg-secondary/30 rounded-xl p-4 mb-4 space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label className="text-[13px]">{t('settings.alerts.rules.formNameLabel')}</Label>
                <Input
                  value={ruleForm.name}
                  onChange={e => setRuleForm(f => ({ ...f, name: e.target.value }))}
                  placeholder={t('settings.alerts.rules.formNamePlaceholder')}
                  className="rounded-xl"
                />
              </div>
              <div className="space-y-1.5">
                <Label className="text-[13px]">{t('settings.alerts.rules.formTypeLabel')}</Label>
                <Select value={ruleForm.type} onValueChange={v => setRuleForm(f => ({ ...f, type: v }))}>
                  <SelectTrigger className="rounded-xl w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {RULE_TYPES.map(rt => (
                      <SelectItem key={rt.value} value={rt.value}>{t(rt.i18nKey)}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            {/* Host metric rules: percentage threshold. Hidden for container_* types. */}
            {!CONTAINER_RULE_TYPES.has(ruleForm.type) && (
              <div className="space-y-1.5">
                <Label className="text-[13px]">{t('settings.alerts.rules.formThresholdLabel')}</Label>
                <Input
                  type="number"
                  min={0}
                  max={100}
                  value={ruleForm.threshold}
                  onChange={e => setRuleForm(f => ({ ...f, threshold: Number(e.target.value) }))}
                  className="rounded-xl"
                />
              </div>
            )}
            {/* Container rules: container name pattern (wildcard, e.g. * or nginx-*) */}
            {CONTAINER_RULE_TYPES.has(ruleForm.type) && (
              <div className="space-y-1.5">
                <Label className="text-[13px]">{t('settings.alerts.rules.formContainerPatternLabel')}</Label>
                <Input
                  value={containerPattern}
                  onChange={e => setContainerPattern(e.target.value)}
                  placeholder={t('settings.alerts.rules.formContainerPatternPlaceholder')}
                  className="rounded-xl"
                />
              </div>
            )}
            {/* Restart loop only: count threshold + observation window */}
            {ruleForm.type === 'container_restart_loop' && (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label className="text-[13px]">{t('settings.alerts.rules.formRestartCountLabel')}</Label>
                  <Input
                    type="number"
                    min={1}
                    value={thresholdCount}
                    onChange={e => setThresholdCount(parseInt(e.target.value || '3', 10))}
                    className="rounded-xl"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label className="text-[13px]">{t('settings.alerts.rules.formWindowLabel')}</Label>
                  <Input
                    type="number"
                    min={30}
                    value={windowSeconds}
                    onChange={e => setWindowSeconds(parseInt(e.target.value || '300', 10))}
                    className="rounded-xl"
                  />
                </div>
              </div>
            )}
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label className="text-[13px]">{t('settings.alerts.rules.formSeverityLabel')}</Label>
                <Select value={ruleForm.severity} onValueChange={v => setRuleForm(f => ({ ...f, severity: v as 'info' | 'warning' | 'critical' }))}>
                  <SelectTrigger className="rounded-xl w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {SEVERITY_OPTIONS.map(s => (
                      <SelectItem key={s.value} value={s.value}>{s.label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label className="text-[13px]">{t('settings.alerts.rules.formCooldownLabel')}</Label>
                <Input
                  type="number"
                  min={0}
                  value={ruleForm.cooldown}
                  onChange={e => setRuleForm(f => ({ ...f, cooldown: Number(e.target.value) }))}
                  className="rounded-xl"
                />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label className="text-[13px]">{t('settings.alerts.rules.formChannelsLabel')}</Label>
              {channels.length === 0 ? (
                <p className="text-[12px] text-muted-foreground">{t('settings.alerts.rules.formChannelsEmpty')}</p>
              ) : (
                <div className="flex flex-wrap gap-2">
                  {channels.map(ch => (
                    <label key={ch.id} className="flex items-center gap-1.5 bg-secondary/50 rounded-lg px-2.5 py-1.5 cursor-pointer">
                      <Checkbox
                        checked={ruleForm.channels.includes(ch.id)}
                        onCheckedChange={() => toggleRuleChannel(ch.id)}
                      />
                      <span className="text-[12px]">{ch.name}</span>
                    </label>
                  ))}
                </div>
              )}
            </div>
            <div className="space-y-1.5">
              <Label className="text-[13px]">{t('settings.alerts.rules.formNodeScopeLabel')}</Label>
              <Select value={ruleForm.node_scope} onValueChange={v => setRuleForm(f => ({ ...f, node_scope: v }))}>
                <SelectTrigger className="rounded-xl w-full max-w-[200px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">{t('settings.alerts.rules.nodeScopeAll')}</SelectItem>
                  <SelectItem value="specific">{t('settings.alerts.rules.nodeScopeSpecific')}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <Button onClick={handleAddRule} disabled={ruleLoading} className="rounded-xl">
              {ruleLoading ? t('settings.alerts.rules.addInProgress') : t('settings.alerts.rules.addButton')}
            </Button>
          </div>
        )}

        {/* Rule list */}
        {rules.length === 0 ? (
          <p className="text-[13px] text-muted-foreground py-4">{t('settings.alerts.rules.empty')}</p>
        ) : (
          <div className="space-y-2">
            {rules.map(rule => {
              const ruleTypeEntry = RULE_TYPES.find(rt => rt.value === rule.type)
              return (
              <div key={rule.id} className="flex items-center justify-between bg-secondary/30 rounded-xl px-4 py-3">
                <div className="flex items-center gap-3 flex-wrap">
                  <span className="text-[13px] font-medium">{rule.name}</span>
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
                    {ruleTypeEntry ? t(ruleTypeEntry.i18nKey) : rule.type}
                  </span>
                  <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${getSeverityStyle(rule.severity)}`}>
                    {rule.severity}
                  </span>
                  <span className="text-[11px] text-muted-foreground">
                    {CONTAINER_RULE_TYPES.has(rule.type)
                      ? t('settings.alerts.rules.summaryCooldown', { cooldown: rule.cooldown })
                      : t('settings.alerts.rules.summaryThresholdCooldown', { threshold: rule.threshold, cooldown: rule.cooldown })}
                  </span>
                  <span className={`inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium ${
                    rule.enabled ? 'bg-[#00c471]/10 text-[#00c471]' : 'bg-secondary text-muted-foreground'
                  }`}>
                    {rule.enabled ? t('common.active') : t('common.disabled')}
                  </span>
                </div>
                <div className="flex items-center gap-1.5 shrink-0">
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[11px]"
                    onClick={() => handleToggleRule(rule)}
                  >
                    {rule.enabled ? t('settings.alerts.rules.actionDisable') : t('settings.alerts.rules.actionEnable')}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[#f04452] hover:text-[#f04452]"
                    onClick={() => handleDeleteRule(rule.id)}
                  >
                    <Trash2 className="h-3 w-3" />
                  </Button>
                </div>
              </div>
              )
            })}
          </div>
        )}
      </div>

      {/* ===== Section 3: 알림 히스토리 ===== */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-[15px] font-semibold">{t('settings.alerts.history.title')}</h3>
            <p className="text-[13px] text-muted-foreground mt-1">{t('settings.alerts.history.description')}</p>
          </div>
          {history.length > 0 && (
            <Button
              variant="outline"
              size="sm"
              className="rounded-xl text-[#f04452] hover:text-[#f04452]"
              onClick={handleClearHistory}
            >
              <Trash2 className="h-3.5 w-3.5 mr-1.5" />
              {t('settings.alerts.history.clearButton')}
            </Button>
          )}
        </div>

        {history.length === 0 ? (
          <p className="text-[13px] text-muted-foreground py-4">{t('settings.alerts.history.empty')}</p>
        ) : (
          <>
            <div className="bg-card rounded-2xl card-shadow overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="text-[11px]">{t('settings.alerts.history.colTime')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.alerts.history.colType')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.alerts.history.colSeverity')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.alerts.history.colMessage')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.alerts.history.colNode')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.alerts.history.colStatus')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {history.map(entry => {
                    const ruleTypeEntry = RULE_TYPES.find(rt => rt.value === entry.type)
                    return (
                    <TableRow key={entry.id}>
                      <TableCell className="text-[12px] text-muted-foreground whitespace-nowrap">
                        {new Date(entry.created_at).toLocaleString()}
                      </TableCell>
                      <TableCell>
                        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
                          {ruleTypeEntry ? t(ruleTypeEntry.i18nKey) : entry.type}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${getSeverityStyle(entry.severity)}`}>
                          {entry.severity}
                        </span>
                      </TableCell>
                      <TableCell className="text-[12px] max-w-[300px] truncate">{entry.message}</TableCell>
                      <TableCell className="text-[12px] text-muted-foreground">{entry.node || '-'}</TableCell>
                      <TableCell>
                        <span className={`text-[12px] ${
                          entry.status === 'resolved' ? 'text-[#00c471]' :
                          entry.status === 'fired' ? 'text-[#f04452]' :
                          'text-muted-foreground'
                        }`}>
                          {entry.status}
                        </span>
                      </TableCell>
                    </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
            {historyTotal > historyLimit && (
              <div className="flex items-center justify-between mt-3">
                <span className="text-[12px] text-muted-foreground">
                  {t('settings.alerts.history.pageIndicator', { page: historyPage, total: Math.ceil(historyTotal / historyLimit) })}
                </span>
                <div className="flex gap-1.5">
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2"
                    disabled={historyPage <= 1}
                    onClick={() => { const p = historyPage - 1; setHistoryPage(p); loadHistory(p) }}
                  >
                    <ChevronLeft className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2"
                    disabled={historyPage >= Math.ceil(historyTotal / historyLimit)}
                    onClick={() => { const p = historyPage + 1; setHistoryPage(p); loadHistory(p) }}
                  >
                    <ChevronRight className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}
