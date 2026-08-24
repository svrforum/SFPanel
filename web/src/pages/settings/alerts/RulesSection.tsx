import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { useApiAction } from '@/hooks/useApiAction'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Checkbox } from '@/components/ui/checkbox'
import { Trash2, Plus, X } from 'lucide-react'
import type { AlertChannel, AlertRule } from '@/types/api'
import { RULE_TYPES, SEVERITY_OPTIONS, getSeverityStyle } from './shared'

// Rule types that operate on containers (not host metrics) and use a JSON
// `condition` payload instead of the simple `threshold` percentage.
const CONTAINER_RULE_TYPES = new Set(['container_down', 'container_oom', 'container_restart_loop', 'container_unhealthy'])

const EMPTY_FORM = {
  name: '', type: 'cpu', threshold: 90, severity: 'warning' as 'info' | 'warning' | 'critical',
  cooldown: 300, channels: [] as number[], node_scope: 'all', nodes: [] as string[],
}

export function RulesSection({ channels }: { channels: AlertChannel[] }) {
  const { t } = useTranslation()
  const confirm = useConfirm()

  const [rules, setRules] = useState<AlertRule[]>([])
  const [showAddRule, setShowAddRule] = useState(false)
  const [ruleForm, setRuleForm] = useState(EMPTY_FORM)
  // Container-rule extra inputs (only used when ruleForm.type is a CONTAINER_RULE_TYPES member)
  const [containerPattern, setContainerPattern] = useState('*')
  const [thresholdCount, setThresholdCount] = useState(3)
  const [windowSeconds, setWindowSeconds] = useState(300)
  // Cluster nodes for the specific-scope multi-select (empty in single-node mode).
  const [clusterNodes, setClusterNodes] = useState<{ id: string; name: string }[]>([])
  // The node whose rules these are. Rules live in the local database of
  // whichever node the request reached — nothing replicates alert_rules — and
  // the evaluator only ever reads its own. A rule scoped to a different node
  // is therefore stored where nothing will evaluate it and never fires, while
  // still listing itself as Active. The picker offers this node only.
  const [localNodeId, setLocalNodeId] = useState<string | null>(null)

  useEffect(() => {
    api.getClusterNodes()
      .then((res) => {
        setClusterNodes((res.nodes ?? []).map((n) => ({ id: n.id, name: n.name || n.id })))
        setLocalNodeId(res.local_id ?? null)
      })
      .catch(() => setClusterNodes([]))
  }, [])

  // Restricted to the node being edited, for the reason above.
  const selectableNodes = localNodeId
    ? clusterNodes.filter((n) => n.id === localNodeId)
    : clusterNodes

  const loadRules = useCallback(() => {
    api.getAlertRules()
      .then(setRules)
      .catch(() => { /* ignore */ })
  }, [])

  useEffect(() => {
    loadRules()
  }, [loadRules])

  const { run: runAddRule, loading: ruleLoading } = useApiAction(
    api.createAlertRule.bind(api),
    {
      successMsg: t('settings.alerts.rules.successAdded'),
      errorMsg: t('settings.alerts.rules.errorAddFailed'),
      onSuccess: () => {
        setShowAddRule(false)
        setRuleForm(EMPTY_FORM)
        setContainerPattern('*')
        setThresholdCount(3)
        setWindowSeconds(300)
        loadRules()
      },
    },
  )

  const { run: runToggleRule } = useApiAction(
    api.updateAlertRule.bind(api),
    { errorMsg: t('settings.alerts.rules.errorToggleFailed'), onSuccess: loadRules },
  )

  const { run: runDeleteRule } = useApiAction(
    api.deleteAlertRule.bind(api),
    {
      successMsg: t('settings.alerts.rules.successDeleted'),
      errorMsg: t('settings.alerts.rules.errorDeleteFailed'),
      onSuccess: loadRules,
    },
  )

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

  async function handleAddRule() {
    if (!ruleForm.name.trim()) { toast.error(t('settings.alerts.rules.errorNameRequired')); return }
    if (ruleForm.channels.length === 0) { toast.error(t('settings.alerts.rules.errorChannelsRequired')); return }
    await runAddRule({
      name: ruleForm.name,
      type: ruleForm.type,
      condition: buildConditionForSubmit(ruleForm.type),
      channel_ids: JSON.stringify(ruleForm.channels),
      severity: ruleForm.severity,
      cooldown: ruleForm.cooldown,
      node_scope: ruleForm.node_scope,
      node_ids: JSON.stringify(ruleForm.nodes),
      enabled: true,
    })
  }

  async function handleToggleRule(rule: AlertRule) {
    await runToggleRule(rule.id, { enabled: !rule.enabled })
  }

  async function handleDeleteRule(id: number) {
    if (!(await confirm({ title: t('settings.alerts.rules.confirmDelete'), danger: true }))) return
    await runDeleteRule(id)
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
                    <SelectItem key={s.value} value={s.value}>{t(s.i18nKey, { defaultValue: s.fallback })}</SelectItem>
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
            {ruleForm.node_scope === 'specific' && (
              <div className="flex flex-wrap gap-1.5 pt-1">
                {selectableNodes.length === 0 ? (
                  <span className="text-[12px] text-muted-foreground">{t('settings.alerts.rules.noNodes')}</span>
                ) : (
                  selectableNodes.map((n) => {
                    const selected = ruleForm.nodes.includes(n.id)
                    return (
                      <button
                        key={n.id}
                        type="button"
                        onClick={() => setRuleForm((f) => ({
                          ...f,
                          nodes: selected ? f.nodes.filter((x) => x !== n.id) : [...f.nodes, n.id],
                        }))}
                        className={`px-2 py-1 rounded-lg text-[12px] border transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${selected ? 'bg-primary/10 border-primary/40 text-primary' : 'border-border text-muted-foreground hover:bg-accent'}`}
                      >
                        {n.name}
                      </button>
                    )
                  })
                )}
              </div>
            )}
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
            const severityEntry = SEVERITY_OPTIONS.find(s => s.value === rule.severity)
            return (
            <div key={rule.id} className="flex items-center justify-between bg-secondary/30 rounded-xl px-4 py-3">
              <div className="flex items-center gap-3 flex-wrap">
                <span className="text-[13px] font-medium">{rule.name}</span>
                <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
                  {ruleTypeEntry ? t(ruleTypeEntry.i18nKey) : rule.type}
                </span>
                <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${getSeverityStyle(rule.severity)}`}>
                  {severityEntry ? t(severityEntry.i18nKey, { defaultValue: severityEntry.fallback }) : rule.severity}
                </span>
                <span className="text-[11px] text-muted-foreground">
                  {CONTAINER_RULE_TYPES.has(rule.type)
                    ? t('settings.alerts.rules.summaryCooldown', { cooldown: rule.cooldown })
                    : t('settings.alerts.rules.summaryThresholdCooldown', { threshold: rule.threshold, cooldown: rule.cooldown })}
                </span>
                <span className={`inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium ${
                  rule.enabled ? 'bg-success/10 text-success' : 'bg-secondary text-muted-foreground'
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
                  className="rounded-xl h-7 px-2 text-destructive hover:text-destructive"
                  onClick={() => handleDeleteRule(rule.id)}
                  aria-label={t('common.delete')}
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
  )
}
