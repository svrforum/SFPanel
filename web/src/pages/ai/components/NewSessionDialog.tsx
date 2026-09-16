import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import type { AIDirs, AIProfile, AISession, AITool, AITools } from '@/types/api'
import type { AILastSession, AITouched } from '@/lib/aiSessions'
import { TOOL_META, aiErrorMessage, aiPrefill, defaultTitle, loginCommandFor, profileErrorMessage, relativeSince, supportsProfiles, toolInstalledFor, toolsFor, untouchedPrefill } from '@/lib/aiSessions'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

const TOOLS: AITool[] = ['claude', 'codex', 'gemini', 'shell']
const lastKey = (node: string) => `sfpanel_ai_last:${node}`

// The two rows of the profile Select that are not a profile: the default
// profile, whose name is the empty string the picker cannot use (Radix reads
// an empty item value as "cleared"), and the row that opens the create
// field. A profile name is letters, digits, dot, dash and underscore only,
// so neither sentinel can collide with one.
const DEFAULT_PROFILE = '(default)'
const NEW_PROFILE = '(new)'

// The default profile's row before the server has described one: the picker
// has to read 기본 rather than show an empty trigger, and a create must not
// append to a list that never arrived — that would leave the default profile
// out of the only place it can be picked back. A row the panel invented has
// no path, which is how the dot below knows to claim nothing about its login.
const DEFAULT_ROW: AIProfile = { name: '', default: true, path: '', logged_in: false }

// Account, then tool, profile, directory, name — the account comes first because it
// decides which tools are installed and which directories are suggested.
// The directory field is a free-text input with the server's suggestions as
// a datalist: recent directories, the compose stacks, the account's home.
// The server validates on submit and its code becomes the inline message.
export function NewSessionDialog({
  open,
  onOpenChange,
  account,
  tools,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  account: string
  tools: AITools | null
  onCreated: (s: AISession) => void
}) {
  const { t, i18n } = useTranslation()
  const node = api.currentNode || 'local'
  const [tool, setTool] = useState<AITool>('claude')
  const [cwd, setCwd] = useState('')
  const [runAs, setRunAs] = useState(account)
  const [title, setTitle] = useState('')
  const [profile, setProfile] = useState('')
  const [profiles, setProfiles] = useState<AIProfile[] | null>(null)
  // null = the picker; a string = the create row, holding what is typed in it.
  const [newName, setNewName] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [dirs, setDirs] = useState<AIDirs | null>(null)
  const [runAsTools, setRunAsTools] = useState<AITools | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const prefilled = useRef(false)
  // What the operator has set by hand since the dialog opened. The prefill is
  // deferred while the account is unresolved and therefore runs again once it
  // arrives; these are the fields it must not take back.
  const touched = useRef<AITouched>({ tool: false, cwd: false })

  // The name and the last error belong to one opening of the dialog, so they
  // are cleared on the closed -> open transition and nowhere else. In
  // particular not in the prefill below, which may have to run again.
  useEffect(() => {
    if (!open) { prefilled.current = false; return }
    setError(null)
    setTitle('')
    touched.current = { tool: false, cwd: false }
  }, [open])

  // Prefill once — but only once it can produce a real account. Opening the
  // dialog before GET /ai/tools resolves used to set runAs to '', which left
  // the account select empty and stopped the effect below on !runAs, and the
  // flag meant it never retried.
  useEffect(() => {
    if (!open || prefilled.current) return
    let last: AILastSession = {}
    try {
      last = JSON.parse(localStorage.getItem(lastKey(node)) || '{}') as AILastSession
    } catch { /* private mode, or a value someone else wrote */ }
    const pre = aiPrefill(last, tools?.accounts, account)
    if (!pre) return
    prefilled.current = true
    const use = untouchedPrefill(pre, touched.current)
    if (use.tool && TOOLS.includes(use.tool)) setTool(use.tool)
    if (use.cwd) setCwd(use.cwd)
    setRunAs(use.runAs)
  }, [open, account, node, tools])

  // Directories and install state both belong to the chosen account, so both
  // are fetched here and both are dropped when it changes. The page's bundle
  // covers only its own account; asking for another is one memoised call per
  // (account, tool) on the server, 10 min.
  const pageAccount = tools?.account
  useEffect(() => {
    if (!open || !runAs) return
    let cancelled = false
    api.getAIDirs(runAs).then((d) => {
      if (cancelled) return
      setDirs(d)
      setCwd((c) => c || d.recent[0] || d.stacks[0] || d.home)
    }).catch(() => setDirs(null))
    if (runAs !== pageAccount) {
      api.getAITools(runAs).then((tl) => { if (!cancelled) setRunAsTools(tl) }).catch(() => { if (!cancelled) setRunAsTools(null) })
    }
    return () => { cancelled = true }
  }, [open, runAs, pageAccount])

  // A profile is per (account, tool): alice's "work" and root's "work" are
  // different directories, and a tool decides whether there are any at all.
  // So a change to either drops the selection back to the default and
  // refetches — carrying a name over would have started the session on a
  // directory belonging to another account, or on one that does not exist.
  const withProfiles = supportsProfiles(tool)
  useEffect(() => {
    setProfile('')
    setNewName(null)
    setProfiles(null)
    if (!open || !runAs || !withProfiles) return
    let cancelled = false
    api.getAIProfiles(runAs, tool)
      .then((r) => { if (!cancelled) setProfiles(r.profiles) })
      .catch(() => { if (!cancelled) setProfiles(null) })
    return () => { cancelled = true }
  }, [open, runAs, tool, withProfiles])

  const createProfile = async () => {
    const name = (newName ?? '').trim()
    if (!name) return
    setCreating(true)
    setError(null)
    try {
      // The server answers with the directory it made, logged_in false: the
      // hint below then tells the operator to log it in, which the panel
      // never does for them.
      const p = await api.createAIProfile({ user: runAs, tool, name })
      setProfiles((list) => [...(list ?? [DEFAULT_ROW]), p])
      setProfile(p.name)
      setNewName(null)
    } catch (err: unknown) {
      setError(profileErrorMessage(err, t))
    } finally {
      setCreating(false)
    }
  }

  const chosen = profiles?.find((p) => (p.default ? '' : p.name) === profile) ?? null
  const shown: AIProfile[] = profiles ?? [DEFAULT_ROW]

  const bundle = toolsFor(runAs, tools, runAsTools)
  const installedFor = (tl: AITool) => toolInstalledFor(bundle, tl)
  useEffect(() => {
    // Switching to an account that lacks the chosen tool must not leave a
    // disabled radio selected.
    if (!installedFor(tool)) setTool('shell')
  }, [runAs, bundle]) // eslint-disable-line react-hooks/exhaustive-deps

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      const s = await api.createAISession({ tool, cwd: cwd.trim(), run_as: runAs, title: title.trim() || undefined, profile: profile || undefined })
      try { localStorage.setItem(lastKey(node), JSON.stringify({ tool, cwd: cwd.trim(), run_as: runAs })) } catch { /* private mode */ }
      onCreated(s)
      onOpenChange(false)
    } catch (err: unknown) {
      setError(aiErrorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('ai.dialog.title')}</DialogTitle>
          <DialogDescription>{t('ai.subtitle')}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {/* Account first: it decides which tools are installed and which
              directories are suggested, so everything below reacts to it. */}
          <div className="space-y-1.5">
            <Label>{t('ai.dialog.account')}</Label>
            <Select value={runAs} onValueChange={(a) => { setRunAs(a); setCwd('') }}>
              <SelectTrigger className="font-mono text-[12px]"><SelectValue /></SelectTrigger>
              <SelectContent>
                {(tools?.accounts ?? [runAs]).map((a) => <SelectItem key={a} value={a} className="font-mono text-[12px]">{a}</SelectItem>)}
              </SelectContent>
            </Select>
            <p className="text-[11px] text-muted-foreground">{t('ai.accountHint')}</p>
          </div>
          <div className="space-y-1.5">
            <Label>{t('ai.dialog.tool')}</Label>
            <div className="grid grid-cols-4 gap-2" role="radiogroup" aria-label={t('ai.dialog.tool')}>
              {TOOLS.map((tl) => {
                const meta = TOOL_META[tl]
                const ok = installedFor(tl)
                return (
                  <button key={tl} type="button" role="radio" aria-checked={tool === tl} disabled={!ok}
                    title={ok ? meta.label : t('ai.dialog.toolNotInstalled', { account: runAs })}
                    onClick={() => { touched.current.tool = true; setTool(tl) }}
                    className={cn('flex flex-col items-center gap-1 rounded-xl border p-2 text-[12px] transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
                      tool === tl ? 'border-primary bg-primary/5' : 'hover:bg-accent', !ok && 'opacity-40 cursor-not-allowed')}>
                    <span className="h-7 w-7 rounded-lg flex items-center justify-center text-[13px] font-bold"
                      style={{ backgroundColor: `${meta.color}1a`, color: meta.color }}>{meta.initial}</span>
                    {meta.label}
                  </button>
                )
              })}
            </div>
          </div>
          {/* Profile: which of the tool's logins the session runs under. The
              account decides which profiles exist, so the field sits between
              the account and the directory; it is absent for a tool with no
              profile support, whose route would answer INVALID_TOOL. */}
          {withProfiles && (
            <div className="space-y-1.5">
              <Label>{t('ai.profiles.label')}</Label>
              {newName === null ? (
                <Select
                  value={profile || DEFAULT_PROFILE}
                  onValueChange={(v) => {
                    if (v === NEW_PROFILE) { setNewName(''); return }
                    setProfile(v === DEFAULT_PROFILE ? '' : v)
                  }}
                >
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {shown.map((p) => {
                      const value = p.default ? DEFAULT_PROFILE : p.name
                      const used = relativeSince(p.last_used_at || '', i18n.language)
                      return (
                        <SelectItem key={value} value={value}>
                          {p.path !== '' && <>
                            <span className={cn('h-1.5 w-1.5 rounded-full', p.logged_in ? 'bg-success' : 'bg-muted-foreground/40')} aria-hidden="true" />
                            <span className="sr-only">{p.logged_in ? t('ai.profiles.loggedIn') : t('ai.profiles.notLoggedIn')}</span>
                          </>}
                          <span className={p.default ? undefined : 'font-mono text-[12px]'}>{p.default ? t('ai.profiles.default') : p.name}</span>
                          {used && <span className="text-[11px] text-muted-foreground">{t('ai.profiles.lastUsed', { when: used })}</span>}
                        </SelectItem>
                      )
                    })}
                    <SelectItem value={NEW_PROFILE}>{t('ai.profiles.create')}</SelectItem>
                  </SelectContent>
                </Select>
              ) : (
                /* A Select row cannot hold a button, so the create row replaces
                   the picker instead of nesting inside it. */
                <div className="flex gap-2">
                  <Input value={newName} onChange={(e) => setNewName(e.target.value)}
                    // A second Enter while the first is in flight would post
                    // the same name again and paint that 409 over a create
                    // that had already succeeded.
                    onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); if (!creating) void createProfile() } }}
                    placeholder={t('ai.profiles.namePlaceholder')} className="font-mono text-[12px]" maxLength={32} spellCheck={false} />
                  <Button variant="outline" className="rounded-xl" onClick={createProfile} disabled={creating || !newName.trim()}>
                    {creating ? <><Loader2 className="animate-spin" aria-hidden="true" />{t('ai.profiles.creating')}</> : t('common.create')}
                  </Button>
                  <Button variant="ghost" className="rounded-xl" onClick={() => setNewName(null)} disabled={creating}>{t('common.cancel')}</Button>
                </div>
              )}
              {chosen && chosen.path !== '' && !chosen.logged_in && (
                <p className="text-[11px] text-warning">{t('ai.profiles.loginHint', { command: loginCommandFor(tool) })}</p>
              )}
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor="ai-cwd">{t('ai.dialog.dir')}</Label>
            <Input id="ai-cwd" list="ai-dir-suggestions" value={cwd} onChange={(e) => { touched.current.cwd = true; setCwd(e.target.value) }}
              placeholder={t('ai.dialog.dirPlaceholder')} className="font-mono text-[12px]" spellCheck={false} />
            <datalist id="ai-dir-suggestions">
              {dirs?.recent.map((d) => <option key={'r' + d} value={d}>{t('ai.dialog.dirRecent')}</option>)}
              {dirs?.stacks.map((d) => <option key={'s' + d} value={d}>{t('ai.dialog.dirStacks')}</option>)}
              {dirs?.home && <option value={dirs.home}>{t('ai.dialog.dirHome')}</option>}
            </datalist>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ai-title">{t('ai.dialog.name')}</Label>
            <Input id="ai-title" value={title} onChange={(e) => setTitle(e.target.value)} placeholder={defaultTitle(tool, cwd || '/', profile)} maxLength={64} />
          </div>
          {error && <p role="alert" className="text-[12px] text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)} disabled={busy}>{t('common.cancel')}</Button>
          <Button className="rounded-xl" onClick={submit} disabled={busy || !cwd.trim()}>
            {busy ? <><Loader2 className="animate-spin" aria-hidden="true" />{t('ai.dialog.creating')}</> : t('ai.dialog.create')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
