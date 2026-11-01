import { useId, useRef, useState, type ChangeEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ClipboardPaste, Eye, EyeOff, FileUp, Loader2, Lock, Play, Plus, X } from 'lucide-react'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { AgentInstallMethod, AgentInstallMode } from '@/types/api'
import { EditorCheck, EditorField } from '../TaskStepCard'
import { builtinText } from '../taskBuiltins'
import { useMounted } from '../useMounted'
import {
  MAX_CONCURRENCY,
  buildInstallRequest,
  emptyInstallForm,
  emptyServerRow,
  formProblemText,
  formProblems,
  lineProblemText,
  parseServerLines,
  problemFromError,
  type FormProblem,
  type InstallForm,
  type InstallServerRow,
  type LineProblem,
} from './installForm'
import { AGENT_INSTALL_TASK_ID } from './installRun'

const TEXTAREA = cn(
  'w-full min-h-24 rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[13px] shadow-xs outline-none',
  'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50 dark:bg-input/30',
)
const SELECT = 'min-h-9 w-28 rounded-md border border-input bg-transparent px-3 text-[13px] dark:bg-input/30 disabled:opacity-50'
// The table's columns on a wide screen: host, port, name, group, remove. On a
// phone each server is a card: the host and its remove button, then the rest.
const ROW_GRID = 'grid grid-cols-3 gap-2 md:grid-cols-[minmax(0,2fr)_5.5rem_minmax(0,1.5fr)_minmax(0,1fr)_2.25rem]'
// No private key comes near this; a file this large is some other file.
const MAX_KEY_FILE_BYTES = 64 * 1024
const METHODS: AgentInstallMethod[] = ['password', 'key', 'tailscale']
const MODES: AgentInstallMode[] = ['auto', 'online', 'offline']
// Where each problem the form or the leader names is shown, apart from a
// server's own, which sits under its row.
const PLACED = new Set(['servers', 'user', 'method', 'password', 'key', 'passphrase', 'sudo_same', 'sudo_password', 'mode', 'concurrency'])

const blankRow = (r: InstallServerRow) => !r.host.trim() && !r.name.trim() && !r.group.trim()
const withoutSecrets = (f: InstallForm): InstallForm => ({ ...f, password: '', key: '', passphrase: '', sudoPassword: '' })

// The agent install's form: the servers (typed, or pasted a line each), the
// one account that logs in to all of them, how to install, then [Preview]
// (the leader stops every server after its pre-flight check, for a look
// before anything is installed) or [Start install]. Either opens the run.
// It is not a task: no task route, run form or task inputs, which a run
// records and every manager keeps. The passwords, the key and its passphrase
// live in this component's state alone and leave the browser in the start's
// body alone: never in an address, a storage or a draft. They are cleared
// as the request is sent, and go with the state when the pane is left.
export function AgentInstallSetup() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const mounted = useMounted()
  const id = useId()

  const [form, setForm] = useState<InstallForm>(emptyInstallForm)
  // The form's problems show once a start was asked for.
  const [attempted, setAttempted] = useState(false)
  // The leader's 400 AGENT_INSTALL_INVALID, on the row or field it names.
  const [refused, setRefused] = useState<FormProblem | null>(null)
  // The start's refusal; retype when it took secrets with it.
  const [error, setError] = useState<{ text: string; retype: boolean } | null>(null)
  const [busy, setBusy] = useState(false)

  const [pasting, setPasting] = useState(false)
  const [pasteText, setPasteText] = useState('')
  const [pasteProblems, setPasteProblems] = useState<{ problem: LineProblem; text: string }[]>([])

  const name = builtinText(t, AGENT_INSTALL_TASK_ID, 'name', '')
  const description = builtinText(t, AGENT_INSTALL_TASK_ID, 'description', '')

  const change = (patch: Partial<InstallForm>) => setForm((f) => ({ ...f, ...patch }))
  // A row the leader named is the row it was sent as: once the table
  // changes, that may be another row.
  const changeServers = (servers: InstallServerRow[]) => {
    setRefused(null)
    change({ servers })
  }
  const changeRow = (index: number, patch: Partial<InstallServerRow>) =>
    changeServers(form.servers.map((r, i) => (i === index ? { ...r, ...patch } : r)))

  const ownProblems = attempted ? formProblems(form) : []
  const problems = [...ownProblems, ...(refused ? [refused] : [])]
  const rowProblems = (index: number) =>
    [...new Set(problems.filter((p) => p.index === index).map((p) => formProblemText(p, t)))]
  const fieldProblem = (field: string) => {
    const p = problems.find((x) => x.index === undefined && x.field === field)
    return p ? formProblemText(p, t) : undefined
  }
  // A refusal for something this form does not show on its own.
  const unplaced = refused && refused.index === undefined && !PLACED.has(refused.field) ? formProblemText(refused, t) : null

  const addPasted = () => {
    const { servers, problems: found } = parseServerLines(pasteText)
    const lines = pasteText.split(/\r?\n/)
    setPasteProblems(found.map((problem) => ({ problem, text: lines[problem.line - 1]?.trim() ?? '' })))
    if (servers.length > 0) changeServers([...form.servers.filter((r) => !blankRow(r)), ...servers])
    setPasteText('')
    if (found.length === 0) setPasting(false)
  }

  const removeRow = (index: number) => {
    const rest = form.servers.filter((_, i) => i !== index)
    changeServers(rest.length > 0 ? rest : [emptyServerRow()])
  }

  const submit = async (review: boolean) => {
    setAttempted(true)
    setRefused(null)
    setError(null)
    if (formProblems(form).length > 0) return
    const sent = form
    const request = buildInstallRequest(sent, review)
    const a = request.account
    const hadSecret = !!(a.password || a.key || a.passphrase || a.sudo_password)
    // The secrets leave with the request: the form forgets them now.
    setForm(withoutSecrets)
    setBusy(true)
    try {
      const { run_id } = await api.startAgentInstall(request)
      if (mounted.current) navigate(`/cluster/tasks/runs/${encodeURIComponent(run_id)}`)
    } catch (err) {
      if (!mounted.current) return
      // Rows are read against the form as it was sent.
      setRefused(problemFromError(err, sent))
      setError({ text: taskErrorMessage(err, t), retype: hadSecret })
    } finally {
      if (mounted.current) setBusy(false)
    }
  }

  const rootUser = form.user.trim() === 'root'
  const methodLabel = (m: AgentInstallMethod) => {
    switch (m) {
      case 'password': return t('cluster.tasks.install.setup.methodPassword')
      case 'key': return t('cluster.tasks.install.setup.methodKey')
      case 'tailscale': return t('cluster.tasks.install.setup.methodTailscale')
    }
  }
  const modeLabel = (m: AgentInstallMode) => {
    switch (m) {
      case 'auto': return t('cluster.tasks.install.setup.modeAuto')
      case 'online': return t('cluster.tasks.install.setup.modeOnline')
      case 'offline': return t('cluster.tasks.install.setup.modeOffline')
    }
  }
  const modeHint = (m: AgentInstallMode) => {
    switch (m) {
      case 'auto': return t('cluster.tasks.install.setup.modeAutoHint')
      case 'online': return t('cluster.tasks.install.setup.modeOnlineHint')
      case 'offline': return t('cluster.tasks.install.setup.modeOfflineHint')
    }
  }

  return (
    <section data-testid="task-pane-install" className="rounded-2xl bg-card p-5 card-shadow space-y-6 min-w-0">
      <div className="space-y-2">
        <h2 className="min-w-0 text-[18px] font-bold break-words">{name}</h2>
        <p className="text-[13px] text-muted-foreground break-words">{description}</p>
      </div>

      <section aria-labelledby={`${id}-servers`} className="space-y-3">
        <h3 id={`${id}-servers`} className="text-[14px] font-semibold">{t('cluster.tasks.install.setup.servers')}</h3>
        <div className="space-y-2">
          <div aria-hidden className={cn(ROW_GRID, 'hidden md:grid text-[12px] font-medium text-muted-foreground')}>
            <span>{t('cluster.tasks.install.setup.host')}</span>
            <span>{t('cluster.tasks.install.setup.port')}</span>
            <span>{t('cluster.tasks.install.setup.name')}</span>
            <span>{t('cluster.tasks.install.setup.group')}</span>
            <span />
          </div>
          {form.servers.map((r, i) => {
            const n = i + 1
            const shown = rowProblems(i)
            return (
              <div
                key={i}
                data-testid={`install-server-${i}`}
                className={cn(ROW_GRID, 'rounded-xl border p-2 md:rounded-none md:border-0 md:p-0')}
              >
                <Input
                  aria-label={t('cluster.tasks.install.setup.rowHost', { n })}
                  value={r.host}
                  onChange={(e) => changeRow(i, { host: e.target.value })}
                  placeholder={t('cluster.tasks.install.setup.host')}
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  aria-invalid={shown.length > 0 || undefined}
                  disabled={busy}
                  className="col-span-2 md:col-span-1 font-mono"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={t('cluster.tasks.install.setup.rowRemove', { n })}
                  onClick={() => removeRow(i)}
                  disabled={busy}
                  className="justify-self-end md:order-last"
                >
                  <X className="h-4 w-4" />
                </Button>
                <Input
                  aria-label={t('cluster.tasks.install.setup.rowPort', { n })}
                  value={r.port}
                  onChange={(e) => changeRow(i, { port: e.target.value })}
                  inputMode="numeric"
                  placeholder="22"
                  autoComplete="off"
                  disabled={busy}
                  className="font-mono"
                />
                <Input
                  aria-label={t('cluster.tasks.install.setup.rowName', { n })}
                  value={r.name}
                  onChange={(e) => changeRow(i, { name: e.target.value })}
                  placeholder={t('cluster.tasks.install.setup.namePlaceholder')}
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  disabled={busy}
                />
                <Input
                  aria-label={t('cluster.tasks.install.setup.rowGroup', { n })}
                  value={r.group}
                  onChange={(e) => changeRow(i, { group: e.target.value })}
                  placeholder={t('cluster.tasks.install.setup.groupPlaceholder')}
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  disabled={busy}
                />
                {shown.length > 0 && (
                  <div className="col-span-full order-last space-y-0.5">
                    {shown.map((text) => <p key={text} className="text-[12px] text-destructive">{text}</p>)}
                  </div>
                )}
              </div>
            )
          })}
          {fieldProblem('servers') && <p className="text-[12px] text-destructive">{fieldProblem('servers')}</p>}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => changeServers([...form.servers, emptyServerRow()])} disabled={busy}>
            <Plus className="h-4 w-4" />
            {t('cluster.tasks.install.setup.addServer')}
          </Button>
          <Button type="button" variant="outline" size="sm" aria-expanded={pasting} onClick={() => setPasting((v) => !v)} disabled={busy}>
            <ClipboardPaste className="h-4 w-4" />
            {t('cluster.tasks.install.setup.paste')}
          </Button>
        </div>
        {pasting && (
          <div className="space-y-2 rounded-xl bg-secondary/40 p-3">
            <EditorField id={`${id}-paste`} label={t('cluster.tasks.install.setup.pasteLabel')} hint={t('cluster.tasks.install.setup.pasteHint')}>
              <textarea
                id={`${id}-paste`}
                value={pasteText}
                onChange={(e) => setPasteText(e.target.value)}
                rows={5}
                placeholder={'192.0.2.21 web-04 web\n192.0.2.22:2222'}
                spellCheck={false}
                autoComplete="off"
                autoCorrect="off"
                autoCapitalize="none"
                className={TEXTAREA}
              />
            </EditorField>
            <div className="flex flex-wrap gap-2">
              <Button type="button" size="sm" onClick={addPasted} disabled={!pasteText.trim()}>
                {t('cluster.tasks.install.setup.pasteAdd')}
              </Button>
              <Button type="button" variant="outline" size="sm" onClick={() => setPasting(false)}>
                {t('common.cancel')}
              </Button>
            </div>
          </div>
        )}
        {pasteProblems.length > 0 && (
          <div data-testid="install-paste-problems" className="rounded-xl bg-warning/10 px-3 py-2 text-[12px] text-warning space-y-1">
            <p className="font-medium">{t('cluster.tasks.install.setup.pasteSkipped')}</p>
            <ul className="space-y-0.5">
              {pasteProblems.map(({ problem, text }) => (
                <li key={problem.line} className="break-words">
                  {lineProblemText(problem, t)} <code className="font-mono opacity-80">{text}</code>
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>

      <section aria-labelledby={`${id}-account`} data-testid="install-account" className="space-y-3">
        <h3 id={`${id}-account`} className="text-[14px] font-semibold">{t('cluster.tasks.install.setup.account')}</h3>
        <div className="max-w-xs">
          <EditorField id={`${id}-user`} label={t('cluster.tasks.install.setup.user')} error={fieldProblem('user')}>
            <Input
              id={`${id}-user`}
              value={form.user}
              onChange={(e) => change({ user: e.target.value })}
              placeholder="ubuntu"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              aria-invalid={!!fieldProblem('user') || undefined}
              disabled={busy}
            />
          </EditorField>
        </div>
        <div className="space-y-1">
          <p id={`${id}-method`} className="text-[12px] font-medium text-muted-foreground">{t('cluster.tasks.install.setup.method')}</p>
          <div role="radiogroup" aria-labelledby={`${id}-method`} className="flex flex-wrap gap-x-4 gap-y-1">
            {METHODS.map((m) => (
              <label key={m} className="flex items-center gap-2 min-h-9 text-[13px] cursor-pointer">
                <input
                  type="radio"
                  name={`${id}-method`}
                  checked={form.method === m}
                  disabled={busy}
                  onChange={() => change({ method: m })}
                  className="accent-primary"
                />
                {methodLabel(m)}
              </label>
            ))}
          </div>
          {fieldProblem('method') && <p className="text-[12px] text-destructive">{fieldProblem('method')}</p>}
        </div>

        {form.method === 'password' && (
          <div className="max-w-xs">
            <SecretField
              id={`${id}-password`}
              label={t('cluster.tasks.install.setup.password')}
              showLabel={t('cluster.tasks.install.setup.showPassword')}
              hideLabel={t('cluster.tasks.install.setup.hidePassword')}
              value={form.password}
              onChange={(password) => change({ password })}
              error={fieldProblem('password')}
              disabled={busy}
            />
          </div>
        )}
        {form.method === 'key' && (
          <KeyFields
            id={id}
            keyText={form.key}
            passphrase={form.passphrase}
            onKey={(key) => change({ key })}
            onPassphrase={(passphrase) => change({ passphrase })}
            keyError={fieldProblem('key')}
            passphraseError={fieldProblem('passphrase')}
            disabled={busy}
          />
        )}
        {form.method === 'tailscale' && (
          <p className="text-[12px] text-muted-foreground max-w-prose">{t('cluster.tasks.install.setup.tailscaleHint')}</p>
        )}

        {rootUser ? (
          <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.install.setup.sudoRoot')}</p>
        ) : (
          <div className="space-y-2">
            {form.method === 'password' && (
              <EditorCheck
                id={`${id}-sudo-same`}
                label={t('cluster.tasks.install.setup.sudoSame')}
                checked={form.sudoSame}
                onChange={(sudoSame) => change({ sudoSame })}
                disabled={busy}
              />
            )}
            {fieldProblem('sudo_same') && <p className="text-[12px] text-destructive">{fieldProblem('sudo_same')}</p>}
            {(form.method !== 'password' || !form.sudoSame) && (
              <div className="max-w-xs">
                <SecretField
                  id={`${id}-sudo`}
                  label={t('cluster.tasks.install.setup.sudoPassword')}
                  showLabel={t('cluster.tasks.install.setup.showSudo')}
                  hideLabel={t('cluster.tasks.install.setup.hideSudo')}
                  value={form.sudoPassword}
                  onChange={(sudoPassword) => change({ sudoPassword })}
                  hint={t('cluster.tasks.install.setup.sudoHint')}
                  error={fieldProblem('sudo_password')}
                  disabled={busy}
                />
              </div>
            )}
          </div>
        )}
      </section>

      <section aria-labelledby={`${id}-how`} className="space-y-3">
        <h3 id={`${id}-how`} className="text-[14px] font-semibold">{t('cluster.tasks.install.setup.how')}</h3>
        <div className="space-y-1">
          <p id={`${id}-mode`} className="text-[12px] font-medium text-muted-foreground">{t('cluster.tasks.install.setup.mode')}</p>
          <div role="radiogroup" aria-labelledby={`${id}-mode`} className="space-y-1">
            {MODES.map((m) => (
              <label key={m} className="flex items-start gap-2 text-[13px] cursor-pointer">
                <input
                  type="radio"
                  name={`${id}-mode`}
                  checked={form.mode === m}
                  disabled={busy}
                  onChange={() => change({ mode: m })}
                  className="accent-primary mt-1"
                />
                <span className="min-w-0">
                  <span className="font-medium">{modeLabel(m)}</span>
                  <span className="block text-[12px] text-muted-foreground">{modeHint(m)}</span>
                </span>
              </label>
            ))}
          </div>
          {fieldProblem('mode') && <p className="text-[12px] text-destructive">{fieldProblem('mode')}</p>}
        </div>
        <EditorField
          id={`${id}-concurrency`}
          label={t('cluster.tasks.install.setup.concurrency')}
          hint={t('cluster.tasks.install.setup.concurrencyHint')}
          error={fieldProblem('concurrency')}
        >
          <select
            id={`${id}-concurrency`}
            value={form.concurrency}
            onChange={(e) => change({ concurrency: Number(e.target.value) })}
            disabled={busy}
            className={cn(SELECT, 'block')}
          >
            {Array.from({ length: MAX_CONCURRENCY }, (_, i) => i + 1).map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </EditorField>
      </section>

      <p className="flex items-start gap-2 rounded-xl bg-secondary/50 px-3 py-2 text-[12px] text-muted-foreground">
        <Lock className="h-4 w-4 mt-0.5 shrink-0" aria-hidden />
        {t('cluster.tasks.install.setup.lock')}
      </p>

      <div className="space-y-2">
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={() => submit(true)} disabled={busy}>
            <Eye className="h-4 w-4" />
            {t('cluster.tasks.install.setup.preview')}
          </Button>
          <Button type="button" onClick={() => submit(false)} disabled={busy}>
            {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
            {t('cluster.tasks.install.setup.start')}
          </Button>
        </div>
        <p className="text-[12px] text-muted-foreground max-w-prose">{t('cluster.tasks.install.setup.previewHint')}</p>
      </div>

      {(error || ownProblems.length > 0) && (
        <div role="alert" data-testid="install-error" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive space-y-1">
          {error && <p className="break-words">{error.text}</p>}
          {unplaced && <p className="break-words">{unplaced}</p>}
          {error?.retype && <p className="text-[12px]">{t('cluster.tasks.install.setup.retype')}</p>}
          {!error && <p className="break-words">{t('cluster.tasks.install.setup.fixProblems')}</p>}
        </div>
      )}
    </section>
  )
}

// A secret typed in: masked unless the operator asks to see it, and kept out
// of the browser's password manager.
function SecretField({ id, label, showLabel, hideLabel, value, onChange, hint, error, disabled }: {
  id: string
  label: string
  showLabel: string
  hideLabel: string
  value: string
  onChange: (value: string) => void
  hint?: string
  error?: string
  disabled?: boolean
}) {
  const [visible, setVisible] = useState(false)
  return (
    <EditorField id={id} label={label} hint={hint} error={error}>
      <div className="relative">
        <Input
          id={id}
          type={visible ? 'text' : 'password'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          aria-invalid={!!error || undefined}
          disabled={disabled}
          className="pr-10"
        />
        <button
          type="button"
          onClick={() => setVisible((v) => !v)}
          className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          aria-label={visible ? hideLabel : showLabel}
        >
          {visible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
        </button>
      </div>
    </EditorField>
  )
}

// The private key, pasted or read from a file, and its passphrase. A text
// area cannot be masked; it is kept from spelling, correction and autofill.
function KeyFields({ id, keyText, passphrase, onKey, onPassphrase, keyError, passphraseError, disabled }: {
  id: string
  keyText: string
  passphrase: string
  onKey: (key: string) => void
  onPassphrase: (passphrase: string) => void
  keyError?: string
  passphraseError?: string
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const file = useRef<HTMLInputElement>(null)
  const [fileError, setFileError] = useState<string | null>(null)

  const readFile = (e: ChangeEvent<HTMLInputElement>) => {
    const chosen = e.target.files?.[0]
    // Cleared, so the same file can be chosen again.
    e.target.value = ''
    if (!chosen) return
    if (chosen.size > MAX_KEY_FILE_BYTES) {
      setFileError(t('cluster.tasks.install.setup.keyFileTooLarge'))
      return
    }
    const reader = new FileReader()
    reader.onload = () => {
      setFileError(null)
      onKey(typeof reader.result === 'string' ? reader.result : '')
    }
    reader.onerror = () => setFileError(t('cluster.tasks.install.setup.keyFileError'))
    reader.readAsText(chosen)
  }

  return (
    <div className="space-y-3">
      <EditorField id={`${id}-key`} label={t('cluster.tasks.install.setup.key')} error={keyError ?? fileError ?? undefined}>
        <textarea
          id={`${id}-key`}
          value={keyText}
          onChange={(e) => onKey(e.target.value)}
          rows={5}
          placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          spellCheck={false}
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="none"
          aria-invalid={!!keyError || undefined}
          disabled={disabled}
          className={TEXTAREA}
        />
      </EditorField>
      <div>
        <input ref={file} type="file" className="hidden" onChange={readFile} tabIndex={-1} aria-hidden />
        <Button type="button" variant="outline" size="sm" onClick={() => file.current?.click()} disabled={disabled}>
          <FileUp className="h-4 w-4" />
          {t('cluster.tasks.install.setup.keyFile')}
        </Button>
      </div>
      <div className="max-w-xs">
        <SecretField
          id={`${id}-passphrase`}
          label={t('cluster.tasks.install.setup.passphrase')}
          showLabel={t('cluster.tasks.install.setup.showPassphrase')}
          hideLabel={t('cluster.tasks.install.setup.hidePassphrase')}
          value={passphrase}
          onChange={onPassphrase}
          hint={t('cluster.tasks.install.setup.passphraseHint')}
          error={passphraseError}
          disabled={disabled}
        />
      </div>
    </div>
  )
}
