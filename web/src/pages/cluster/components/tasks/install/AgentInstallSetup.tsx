import { useEffect, useId, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ClipboardPaste, Eye, Loader2, Lock, Network, Play, Plus, Settings2, X } from 'lucide-react'
import { api, type ApiError } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { AgentInstallLogin, AgentInstallMode, AgentInstallTailnet, AgentInstallTailnetPeer } from '@/types/api'
import { EditorCheck, EditorField } from '../TaskStepCard'
import { builtinText } from '../taskBuiltins'
import { useMounted } from '../useMounted'
import { AccountFields } from './AccountFields'
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
import {
  TYPED, defaultLogin, loginBody, loginErrorText, loginLabel, loginNameProblem, loginNameRefusal, loginNameText, loginSummary,
  readLastLogin, rememberLogin, withLogin,
} from './installLogins'
import { AGENT_INSTALL_TASK_ID } from './installRun'
import { addPicks, methodAfterPicks } from './installTailnet'
import { InstallLoginsDialog } from './InstallLoginsDialog'
import { TailnetPickDialog } from './TailnetPickDialog'

const TEXTAREA = cn(
  'w-full min-h-24 rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[13px] shadow-xs outline-none',
  'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50 dark:bg-input/30',
)
const SELECT = 'min-h-9 w-28 rounded-md border border-input bg-transparent px-3 text-[13px] dark:bg-input/30 disabled:opacity-50'
// The table's columns on a wide screen: host, port, name, group, remove. On a
// phone each server is a card: the host and its remove button, then the rest.
const ROW_GRID = 'grid grid-cols-3 gap-2 md:grid-cols-[minmax(0,2fr)_5.5rem_minmax(0,1.5fr)_minmax(0,1fr)_2.25rem]'
const MODES: AgentInstallMode[] = ['auto', 'online', 'offline']
// Where each problem the form or the leader names is shown, apart from a
// server's own, which sits under its row.
const PLACED = new Set(['servers', 'user', 'method', 'password', 'key', 'passphrase', 'sudo_same', 'sudo_password', 'mode', 'concurrency'])

const blankRow = (r: InstallServerRow) => !r.host.trim() && !r.name.trim() && !r.group.trim()
const withoutSecrets = (f: InstallForm): InstallForm => ({ ...f, password: '', key: '', passphrase: '', sudoPassword: '' })

// The agent install's form: the servers (typed, pasted a line each, or picked
// from the leader's tailnet), the one account that logs in to all of them —
// typed in, or a login the cluster keeps — how to install, then [Preview]
// (the leader stops every server after its pre-flight check, for a look
// before anything is installed) or [Start install]. Either opens the run.
// It is not a task: no task route, run form or task inputs, which a run
// records and every manager keeps. The passwords, the key and its passphrase
// typed here live in this component's state alone and leave the browser in a
// request's body alone: the start's, or a saved login's when the operator
// ticks [Save this login]. Never in an address, a storage or a draft. A saved
// login's secrets never come back to the browser: the pane knows whether each
// is stored, and a start names the login by its ID alone. That ID, of the
// login the last install started with or saved, is all this browser keeps. A
// start the leader refuses, or one that never reached it, keeps the typed
// secrets, so the operator puts the cause right and starts again without
// typing them twice. A start the leader took clears them. A save does not:
// the install still starts with the account as typed. And they go with the
// state when the pane is left.
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
  // The start's refusal.
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const [pasting, setPasting] = useState(false)
  const [pasteText, setPasteText] = useState('')
  const [pasteProblems, setPasteProblems] = useState<{ problem: LineProblem; text: string }[]>([])

  // The tailnet asked for by [Pick from Tailscale] while its dialog is open.
  const [picking, setPicking] = useState<Promise<AgentInstallTailnet> | null>(null)
  // What the last pick did to the login, until the login is changed by hand.
  const [pickNote, setPickNote] = useState<{ switched: boolean; withoutSsh: string[] } | null>(null)

  // The saved logins, once read (null until then), or why they could not be.
  const [logins, setLogins] = useState<AgentInstallLogin[] | null>(null)
  const [loginsError, setLoginsError] = useState<string | null>(null)
  // The saved login the install takes, or TYPED for the account typed in.
  const [chosen, setChosen] = useState(TYPED)
  // [Save this login] for an account typed in, the name to save it under,
  // and the leader's refusal of that name.
  const [save, setSave] = useState(false)
  const [saveName, setSaveName] = useState('')
  const [nameRefused, setNameRefused] = useState<string | null>(null)
  const [managing, setManaging] = useState(false)
  // Whether the account was typed in, or its method changed by a pick,
  // before the saved logins came: it is not swapped for a saved one then.
  const accountTouched = useRef(false)
  // The login the typed account was saved as, which the next visit starts
  // on once an install starts: a start refused after the save leaves the
  // account typed, and the next one saves nothing.
  const savedAs = useRef<string | null>(null)

  // Read as the pane opens. It starts on the login the last install took or
  // saved, else the first saved; a list that cannot be read leaves typing
  // alone, and so does an account typed while the list was on its way.
  useEffect(() => {
    let live = true
    api.agentInstallLogins().then(
      (list) => {
        if (!live) return
        setLogins(list)
        if (!accountTouched.current) setChosen(defaultLogin(list, readLastLogin()))
      },
      (err) => { if (live) setLoginsError(taskErrorMessage(err, t)) },
    )
    return () => { live = false }
  }, [t])

  const name = builtinText(t, AGENT_INSTALL_TASK_ID, 'name', '')
  const description = builtinText(t, AGENT_INSTALL_TASK_ID, 'description', '')

  const change = (patch: Partial<InstallForm>) => setForm((f) => ({ ...f, ...patch }))
  // The login changed by hand: what a pick said of it no longer holds.
  const changeAccount = (patch: Partial<InstallForm>) => {
    accountTouched.current = true
    if (patch.method !== undefined) setPickNote(null)
    change(patch)
  }

  const selected = chosen === TYPED ? undefined : logins?.find((l) => l.id === chosen)
  const canSave = logins !== null && !loginsError
  const saving = !selected && save && canSave
  const choose = (value: string) => {
    setChosen(value)
    setPickNote(null)
  }
  // The list as the dialog left it: a login chosen that it no longer has
  // gives way to the default.
  const replaceLogins = (list: AgentInstallLogin[]) => {
    const last = readLastLogin()
    setLogins(list)
    setChosen((c) => (c === TYPED || list.some((l) => l.id === c) ? c : defaultLogin(list, last)))
  }
  // A start refused because its login is gone: the list again, and the default.
  const reloadLogins = () => {
    api.agentInstallLogins().then(
      (list) => {
        if (!mounted.current) return
        setLogins(list)
        setChosen(defaultLogin(list, readLastLogin()))
      },
      (err) => {
        if (!mounted.current) return
        setLoginsError(taskErrorMessage(err, t))
        setChosen(TYPED)
      },
    )
  }
  // A row the leader named is the row it was sent as: once the table
  // changes, that may be another row.
  const changeServers = (servers: InstallServerRow[]) => {
    setRefused(null)
    change({ servers })
  }
  const changeRow = (index: number, patch: Partial<InstallServerRow>) =>
    changeServers(form.servers.map((r, i) => (i === index ? { ...r, ...patch } : r)))

  const ownProblems = attempted ? formProblems(form, Boolean(selected)) : []
  // The name a typed account is saved under, once a start was asked for.
  const nameProblem = attempted && saving ? loginNameProblem(saveName, logins ?? []) : null
  const nameError = nameRefused ?? (nameProblem ? loginNameText(nameProblem, t) : undefined)
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

  // Devices picked from the leader's tailnet: a row each, and Tailscale SSH
  // as the typed login when every one added runs it. A saved login chosen
  // stays the account.
  const addTailnet = (picks: AgentInstallTailnetPeer[]) => {
    setPicking(null)
    const { servers, added } = addPicks(form.servers, picks)
    if (added.length === 0) return
    setRefused(null)
    if (selected) {
      change({ servers })
      setPickNote(null)
      return
    }
    const after = methodAfterPicks(form.method, added)
    if (after.switched) accountTouched.current = true
    change({ servers, method: after.method })
    setPickNote(after.switched || after.withoutSsh.length > 0 ? after : null)
  }

  const removeRow = (index: number) => {
    const rest = form.servers.filter((_, i) => i !== index)
    changeServers(rest.length > 0 ? rest : [emptyServerRow()])
  }

  // With [Save this login] ticked the account is saved first, and a save
  // refused starts nothing. The install then starts with the account as
  // typed, not the saved login: the leader limits where a saved login's run
  // may go, and the operator typed this one for these servers.
  const submit = async (review: boolean) => {
    setAttempted(true)
    setRefused(null)
    setError(null)
    setNameRefused(null)
    if (formProblems(form, Boolean(selected)).length > 0) return
    if (saving && loginNameProblem(saveName, logins ?? [])) return
    const sent = form
    const login = selected?.id
    setBusy(true)
    try {
      if (saving) {
        let saved: AgentInstallLogin
        try {
          saved = await api.createAgentInstallLogin(loginBody(saveName, sent))
        } catch (err) {
          if (!mounted.current) return
          // Nothing was saved and nothing starts. The secrets stay.
          const onName = loginNameRefusal(err, t)
          if (onName) {
            setNameRefused(onName)
          } else {
            setRefused(problemFromError(err, sent))
            setError(loginErrorText(err, t))
          }
          return
        }
        if (!mounted.current) return
        // The cluster keeps it now: listed, and the box unticked, so a
        // second start does not save it again. The account stays as typed.
        setLogins((list) => withLogin(list ?? [], saved))
        setSave(false)
        setSaveName('')
        savedAs.current = saved.id
      }
      const { run_id } = await api.startAgentInstall(buildInstallRequest(sent, review, login))
      if (!mounted.current) return
      const last = login ?? savedAs.current
      if (last) rememberLogin(last)
      // The leader holds them now: the form forgets them.
      setForm(withoutSecrets)
      navigate(`/cluster/tasks/runs/${encodeURIComponent(run_id)}`)
    } catch (err) {
      if (!mounted.current) return
      if (login && (err as ApiError)?.code === 'AGENT_INSTALL_LOGIN_NOT_FOUND') {
        // Deleted since the list was read: the list again, to choose from.
        setError(t('cluster.tasks.install.logins.gone'))
        reloadLogins()
        return
      }
      // Rows are read against the form as it was sent. The secrets stay.
      setRefused(problemFromError(err, sent))
      setError(taskErrorMessage(err, t))
    } finally {
      if (mounted.current) setBusy(false)
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
          <Button type="button" variant="outline" size="sm" onClick={() => setPicking(api.agentInstallTailnet())} disabled={busy}>
            <Network className="h-4 w-4" />
            {t('cluster.tasks.install.setup.tailnet')}
          </Button>
        </div>
        {picking && <TailnetPickDialog tailnet={picking} onClose={() => setPicking(null)} onAdd={addTailnet} />}
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
        {pickNote && (
          <p
            data-testid="install-tailnet-note"
            className={cn(
              'rounded-xl px-3 py-2 text-[12px] break-words',
              pickNote.switched ? 'bg-secondary/50 text-muted-foreground' : 'bg-warning/10 text-warning',
            )}
          >
            {pickNote.switched
              ? t('cluster.tasks.install.tailnet.switched')
              : t('cluster.tasks.install.tailnet.withoutSsh', { servers: pickNote.withoutSsh.join(', ') })}
          </p>
        )}
      </section>

      <section aria-labelledby={`${id}-account`} data-testid="install-account" className="space-y-3">
        <h3 id={`${id}-account`} className="text-[14px] font-semibold">{t('cluster.tasks.install.setup.account')}</h3>
        {loginsError ? (
          <p data-testid="install-logins-error" className="text-[12px] text-muted-foreground break-words max-w-prose">
            {t('cluster.tasks.install.logins.loadFailed', { reason: loginsError })}
          </p>
        ) : (
          <EditorField id={`${id}-login`} label={t('cluster.tasks.install.logins.label')}>
            <div className="flex flex-wrap items-center gap-2">
              <select
                id={`${id}-login`}
                value={selected ? selected.id : TYPED}
                onChange={(e) => choose(e.target.value)}
                disabled={busy || logins === null}
                className={cn(SELECT, 'w-full max-w-full sm:w-auto')}
              >
                <option value={TYPED}>{t('cluster.tasks.install.logins.typed')}</option>
                {(logins ?? []).map((l) => <option key={l.id} value={l.id}>{loginLabel(l, t)}</option>)}
              </select>
              {logins && logins.length > 0 && (
                <Button type="button" variant="outline" size="sm" onClick={() => setManaging(true)} disabled={busy}>
                  <Settings2 className="h-4 w-4" />
                  {t('cluster.tasks.install.logins.manage')}
                </Button>
              )}
            </div>
          </EditorField>
        )}
        {managing && logins && <InstallLoginsDialog logins={logins} onChange={replaceLogins} onClose={() => setManaging(false)} />}
        {selected ? (
          <p data-testid="install-login-summary" className="text-[13px] break-words">{loginSummary(selected, t)}</p>
        ) : (
          <>
            <AccountFields id={id} value={form} onChange={changeAccount} problem={fieldProblem} disabled={busy} />
            {canSave && (
              <div className="space-y-2">
                <EditorCheck
                  id={`${id}-save`}
                  label={t('cluster.tasks.install.logins.save')}
                  checked={save}
                  onChange={(on) => {
                    setSave(on)
                    setNameRefused(null)
                  }}
                  disabled={busy}
                />
                {save && (
                  <div className="max-w-xs">
                    <EditorField
                      id={`${id}-login-name`}
                      label={t('cluster.tasks.install.logins.name')}
                      hint={t('cluster.tasks.install.logins.nameHint')}
                      error={nameError}
                    >
                      <Input
                        id={`${id}-login-name`}
                        value={saveName}
                        onChange={(e) => {
                          setSaveName(e.target.value)
                          setNameRefused(null)
                        }}
                        placeholder={t('cluster.tasks.install.logins.namePlaceholder')}
                        autoComplete="off"
                        spellCheck={false}
                        aria-invalid={!!nameError || undefined}
                        disabled={busy}
                      />
                    </EditorField>
                  </div>
                )}
              </div>
            )}
          </>
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
        {selected || saving ? t('cluster.tasks.install.logins.lock') : t('cluster.tasks.install.setup.lock')}
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

      {(error || ownProblems.length > 0 || nameError) && (
        <div role="alert" data-testid="install-error" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive space-y-1">
          {error && <p className="break-words">{error}</p>}
          {unplaced && <p className="break-words">{unplaced}</p>}
          {!error && <p className="break-words">{t('cluster.tasks.install.setup.fixProblems')}</p>}
        </div>
      )}
    </section>
  )
}
