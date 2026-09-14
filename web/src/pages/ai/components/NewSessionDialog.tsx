import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import type { AIDirs, AISession, AITool, AITools } from '@/types/api'
import type { AILastSession, AITouched } from '@/lib/aiSessions'
import { TOOL_META, aiErrorMessage, aiPrefill, defaultTitle, toolInstalledFor, toolsFor, untouchedPrefill } from '@/lib/aiSessions'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

const TOOLS: AITool[] = ['claude', 'codex', 'gemini', 'shell']
const lastKey = (node: string) => `sfpanel_ai_last:${node}`

// Account, then tool, directory, name — the account comes first because it
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
  const { t } = useTranslation()
  const node = api.currentNode || 'local'
  const [tool, setTool] = useState<AITool>('claude')
  const [cwd, setCwd] = useState('')
  const [runAs, setRunAs] = useState(account)
  const [title, setTitle] = useState('')
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
      const s = await api.createAISession({ tool, cwd: cwd.trim(), run_as: runAs, title: title.trim() || undefined })
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
            <Input id="ai-title" value={title} onChange={(e) => setTitle(e.target.value)} placeholder={defaultTitle(tool, cwd || '/')} maxLength={64} />
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
