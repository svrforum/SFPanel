import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { RefreshCw } from 'lucide-react'
import { StatusPill } from '@/components/StatusPill'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { api, type ApiError } from '@/lib/api'
import { agentLabel } from '@/lib/codeMessages'
import type { AgentSettingsInput, AgentSettingsView } from '@/types/api'
import { actionState, deployHref, holdsDescribedBy, type ActionHold, type AgentName } from './agentsModel'
import { ActionLink, HoldLines } from './AgentServerTable'

/** A connection value by the name a refusal gives it in details.field. */
type Field = 'zabbix.server' | 'zabbix.server_active' | 'zabbix.host_metadata' | 'wazuh.manager' | 'wazuh.groups' | 'wazuh.version'

const FIELDS: readonly Field[] = ['zabbix.server', 'zabbix.server_active', 'zabbix.host_metadata', 'wazuh.manager', 'wazuh.groups', 'wazuh.version']

const EMPTY: AgentSettingsInput = { zabbix: { server: '', server_active: '', host_metadata: '' }, wazuh: { manager: '', groups: '', version: '' } }

/** The values alone: what was saved, and by whom, is the server's to set. */
const inputOf = (s: AgentSettingsView): AgentSettingsInput => ({
  zabbix: { server: s.zabbix.server, server_active: s.zabbix.server_active, host_metadata: s.zabbix.host_metadata },
  wazuh: { manager: s.wazuh.manager, groups: s.wazuh.groups, version: s.wazuh.version },
})

const read = (v: AgentSettingsInput, f: Field) => {
  const [agent, key] = f.split('.') as [AgentName, string]
  return (v[agent] as Record<string, string>)[key]
}

const write = (v: AgentSettingsInput, f: Field, value: string): AgentSettingsInput => {
  const [agent, key] = f.split('.') as [AgentName, string]
  return { ...v, [agent]: { ...v[agent], [key]: value } }
}

interface Refusal {
  message: string
  /** The value refused, under its field; none for a refusal of the whole save. */
  field?: Field
  /** The server's reason after the field's name, when it gave one. */
  why?: string
}

function refusal(err: unknown): Refusal {
  const e = err as ApiError
  const message = err instanceof Error ? err.message : String(err)
  const field = FIELDS.find((f) => f === e?.details?.field)
  if (e?.code !== 'SERVER_AGENT_SETTINGS_INVALID' || !field) return { message }
  const sent = e.serverMessage ?? ''
  const why = sent.startsWith(`${field}: `) ? sent.slice(field.length + 2) : undefined
  return { message, field, why }
}

/**
 * The cluster's connection values, one set for every server, saved with one
 * press; below them, a re-apply of each agent's values to every server that
 * has it. A run takes the saved values, so while the card holds others the
 * re-apply (and the table's deploy) is off and says why.
 */
export function AgentSettingsCard({ settings, loadError, onRetry, onSaved, draft, onDraft, dirty, reapply }: {
  settings: AgentSettingsView | null
  loadError: string | null
  onRetry: () => void
  onSaved: (view: AgentSettingsView) => void
  /** Edited and not yet saved; null shows the saved values. */
  draft: AgentSettingsInput | null
  onDraft: (draft: AgentSettingsInput | null) => void
  /** The draft differs from the saved values. */
  dirty: boolean
  /** The servers each agent's re-apply would run on. */
  reapply: Record<AgentName, string[]>
}) {
  const { t } = useTranslation()
  const ids = useId()
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [refused, setRefused] = useState<Refusal | null>(null)
  const values = draft ?? (settings ? inputOf(settings) : EMPTY)

  const change = (f: Field, value: string) => {
    onDraft(write(values, f, value))
    setSaved(false)
    setRefused(null)
  }

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setSaving(true)
    setSaved(false)
    setRefused(null)
    try {
      const view = await api.putAgentSettings(values)
      onSaved(view)
      onDraft(null)
      setSaved(true)
    } catch (err) {
      setRefused(refusal(err))
    } finally {
      setSaving(false)
    }
  }

  const fieldId = (f: Field) => `${ids}-${f.replace('.', '-')}`
  const versionNoteId = `${ids}-version-note`

  const field = (f: Field, label: string, note?: string) => {
    const id = fieldId(f)
    const mine = refused?.field === f ? refused : null
    const describedBy = [mine ? `${id}-error` : '', note ?? ''].filter(Boolean).join(' ') || undefined
    return (
      <div className="space-y-1.5">
        <label htmlFor={id} className="text-[12px] font-medium text-muted-foreground">{label}</label>
        <Input
          id={id}
          value={read(values, f)}
          onChange={(e) => change(f, e.target.value)}
          disabled={!settings}
          aria-invalid={!!mine}
          aria-describedby={describedBy}
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          className="h-9 rounded-xl border-0 bg-secondary/50 text-base md:text-[13px]"
        />
        {mine && (
          <div id={`${id}-error`} className="space-y-0.5">
            <p className="break-words text-[12px] text-destructive">{mine.message}</p>
            {mine.why && <p className="break-words text-[12px] text-muted-foreground">{mine.why}</p>}
          </div>
        )}
      </div>
    )
  }

  // A re-apply with no server says so in its own label (0); the other reasons have a line.
  const reapplyState = {
    zabbix: actionState('zabbix', { settings, dirty, servers: reapply.zabbix.length }),
    wazuh: actionState('wazuh', { settings, dirty, servers: reapply.wazuh.length }),
  }
  const unsavedId = `${ids}-unsaved`
  const valuesId = (agent: AgentName) => `${ids}-values-${agent}`
  const lineOf = (agent: AgentName) => (hold: ActionHold) => (hold === 'unsaved' ? unsavedId : hold === 'values' ? valuesId(agent) : undefined)
  const reapplyLink = (agent: AgentName, label: string) => (
    <ActionLink to={deployHref(agent, reapply[agent])} enabled={reapplyState[agent].enabled} describedBy={holdsDescribedBy(reapplyState[agent].holds, lineOf(agent))}>
      {label}
    </ActionLink>
  )

  return (
    <section aria-labelledby={`${ids}-title`} className="space-y-4 rounded-2xl bg-card p-4 card-shadow md:p-5">
      <div className="flex flex-wrap items-center gap-2">
        <h2 id={`${ids}-title`} className="text-[15px] font-semibold">{t('agents.settings.title')}</h2>
        <StatusPill tone="secondary">{t('agents.settings.shared')}</StatusPill>
      </div>

      {loadError && !settings ? (
        <div role="alert" className="flex flex-wrap items-center gap-2">
          <p className="break-words text-[13px] text-destructive">{t('agents.readError', { error: loadError })}</p>
          <Button variant="outline" size="sm" className="rounded-xl" onClick={onRetry}>
            <RefreshCw aria-hidden="true" />
            {t('agents.retry')}
          </Button>
        </div>
      ) : (
        <form onSubmit={save} className="space-y-4" noValidate>
          <div className="grid gap-4 md:grid-cols-2">
            <fieldset className="min-w-0 space-y-3">
              <legend className="mb-3 text-[13px] font-semibold">{agentLabel('zabbix')}</legend>
              {field('zabbix.server', t('agents.settings.zabbixServer'))}
              {field('zabbix.server_active', t('agents.settings.zabbixActive'))}
              {field('zabbix.host_metadata', t('agents.settings.zabbixMetadata'))}
            </fieldset>
            <fieldset className="min-w-0 space-y-3">
              <legend className="mb-3 text-[13px] font-semibold">{agentLabel('wazuh')}</legend>
              {field('wazuh.manager', t('agents.settings.wazuhManager'))}
              {field('wazuh.groups', t('agents.settings.wazuhGroups'))}
              {field('wazuh.version', t('agents.settings.wazuhVersion'), versionNoteId)}
              <p id={versionNoteId} className="text-[12px] text-muted-foreground">{t('agents.settings.versionNote')}</p>
            </fieldset>
          </div>
          <p className="text-[12px] text-muted-foreground">{t('agents.settings.enrollNote')}</p>
          <div className="flex flex-wrap items-center gap-3">
            <Button type="submit" className="rounded-xl" disabled={!settings || saving}>
              {saving ? t('common.saving') : t('agents.settings.save')}
            </Button>
            <span role="status" className="text-[13px] text-success">{saved ? t('agents.settings.saved') : ''}</span>
            {refused && !refused.field && <p role="alert" className="break-words text-[13px] text-destructive">{refused.message}</p>}
          </div>
        </form>
      )}

      <div className="space-y-1.5 border-t border-border pt-4">
        <div className="flex flex-wrap gap-2">
          {reapplyLink('zabbix', t('agents.actions.reapplyZabbix', { count: reapply.zabbix.length }))}
          {reapplyLink('wazuh', t('agents.actions.reapplyWazuh', { count: reapply.wazuh.length }))}
        </div>
        <HoldLines holds={{ zabbix: reapplyState.zabbix.holds, wazuh: reapplyState.wazuh.holds }} unsavedId={unsavedId} valuesId={valuesId} />
      </div>
    </section>
  )
}
