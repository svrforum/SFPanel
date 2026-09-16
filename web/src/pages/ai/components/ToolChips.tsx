import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUpCircle, CheckCircle2, Download, KeyRound, Loader2, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { AIProfile, AITools } from '@/types/api'
import { TOOL_META, aiErrorMessage, relativeSince, supportsProfiles } from '@/lib/aiSessions'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useConfirm } from '@/components/ConfirmDialog'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { streamErrorMessage, type SSEOutput } from '@/components/OutputDialog'

type CliTool = 'claude' | 'codex' | 'gemini'
const CLI_TOOLS: CliTool[] = ['claude', 'codex', 'gemini']

/**
 * The 프로파일 block of one chip's dropdown: the logins this account keeps for
 * this tool, and a field to add one.
 *
 * It lives inside the DropdownMenuContent, which Radix mounts when the chip
 * opens and unmounts when it closes, so the list is fetched on the operator's
 * click and never on page load — three chips would otherwise have meant three
 * requests nobody asked for. The same unmount is why a delete reports through
 * a toast: opening the confirmation dialog is an interaction outside the menu,
 * which closes it, and by the time the answer arrives this component is gone.
 * Refreshing the session list is therefore the page's job, not this one's.
 */
function ProfileSection({ tool, account, onSessionsChanged }: {
  tool: CliTool
  account: string
  onSessionsChanged: () => void
}) {
  const { t, i18n } = useTranslation()
  const confirm = useConfirm()
  const [profiles, setProfiles] = useState<AIProfile[] | null>(null)
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    let cancelled = false
    api.getAIProfiles(account, tool)
      .then((r) => { if (!cancelled) { setProfiles(r.profiles); setError(null) } })
      .catch((err: unknown) => { if (!cancelled) setError(aiErrorMessage(err, t)) })
    return () => { cancelled = true }
  }, [account, tool, t])
  useEffect(() => load(), [load])

  const create = async () => {
    const value = name.trim()
    if (!value) return
    setBusy(true)
    setError(null)
    try {
      // The server answers with the directory it made; the list is re-read
      // rather than appended to, so a row the panel invented can never
      // disagree with what is on disk.
      await api.createAIProfile({ user: account, tool, name: value })
      setName('')
      load()
    } catch (err: unknown) {
      setError(aiErrorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  const remove = async (p: AIProfile) => {
    const ok = await confirm({
      title: t('ai.profiles.deleteConfirmTitle', { name: p.name }),
      description: t('ai.profiles.deleteConfirmDesc', { account, tool: TOOL_META[tool].label }),
      confirmLabel: t('ai.profiles.delete'),
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteAIProfile(account, tool, p.name)
      toast.success(t('ai.profiles.deleted', { name: p.name }))
      load()
      // A session that ran on the profile still names it on its tab.
      onSessionsChanged()
    } catch (err: unknown) {
      // AI_PROFILE_IN_USE lands here: the panel refuses to delete a profile a
      // live session is on, and the toast is what the operator still sees
      // after the confirmation closed this menu.
      toast.error(aiErrorMessage(err, t))
    }
  }

  return (
    <div className="space-y-1.5">
      <DropdownMenuLabel className="p-0 text-[12px] text-muted-foreground">{t('ai.profiles.manage')}</DropdownMenuLabel>
      {profiles === null ? (
        error === null && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" aria-label={t('ai.tools.checking')} />
      ) : (
        <ul className="space-y-1">
          {profiles.map((p) => {
            const used = relativeSince(p.last_used_at || '', i18n.language)
            return (
              <li key={p.path} className="flex items-center gap-1.5 text-[12px]">
                <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', p.logged_in ? 'bg-success' : 'bg-muted-foreground/40')} aria-hidden="true" />
                <span className="sr-only">{p.logged_in ? t('ai.profiles.loggedIn') : t('ai.profiles.notLoggedIn')}</span>
                <span className={cn('truncate min-w-0', !p.default && 'font-mono')}>{p.default ? t('ai.profiles.default') : p.name}</span>
                <span className="ml-auto flex items-center gap-1 shrink-0">
                  {used && <span className="text-[11px] text-muted-foreground">{t('ai.profiles.lastUsed', { when: used })}</span>}
                  {/* The default profile is the tool's own directory, which is
                      not the panel's to remove — the route refuses it too. */}
                  {!p.default && (
                    <Button variant="ghost" size="sm" className="h-6 w-6 p-0 text-muted-foreground hover:text-destructive"
                      aria-label={t('ai.profiles.delete')} title={t('ai.profiles.delete')}
                      disabled={busy} onClick={() => void remove(p)}>
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  )}
                </span>
              </li>
            )
          })}
        </ul>
      )}
      <div className="flex items-center gap-1.5">
        <Input value={name} onChange={(e) => setName(e.target.value)}
          // A menu treats bare characters as typeahead and moves focus to the
          // item they match, so the keystrokes have to stop at the input.
          // Escape is the exception: it still belongs to the menu, which is
          // how the operator closes the chip from the keyboard.
          onKeyDown={(e) => {
            if (e.key === 'Escape') return
            e.stopPropagation()
            if (e.key === 'Enter') { e.preventDefault(); void create() }
          }}
          placeholder={t('ai.profiles.namePlaceholder')} className="h-7 font-mono text-[12px]" maxLength={32} spellCheck={false}
          aria-label={t('ai.profiles.label')} />
        <Button variant="outline" size="sm" className="h-7 rounded-lg shrink-0" disabled={busy || !name.trim()} onClick={() => void create()}>
          {busy ? <Loader2 className="animate-spin" aria-hidden="true" /> : null}
          {busy ? t('ai.profiles.creating') : t('common.create')}
        </Button>
      </div>
      {error && <p role="alert" className="text-[11px] text-destructive">{error}</p>}
    </div>
  )
}

// Header row of the AI page: which account the page is about, and one chip
// per CLI showing what THAT account's login shell runs. Install/update
// stream into the shared OutputDialog exactly as the packages page does.
export function ToolChips({
  tools,
  account,
  onAccountChange,
  onChanged,
  onSessionsChanged,
  output,
}: {
  tools: AITools | null
  account: string
  onAccountChange: (account: string) => void
  onChanged: () => void
  onSessionsChanged: () => void
  output: SSEOutput
}) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState<CliTool | null>(null)

  // Before GET /ai/tools resolves, `account` is '' (no per-node localStorage
  // entry yet). Radix throws on <SelectItem value="">, and a closed
  // <SelectContent> still renders its items into a detached fragment, so an
  // empty account must never become an item — an empty list plus the
  // placeholder is what the trigger shows during the probe. '' stays legal as
  // the Root value; it is what selects the placeholder.
  const accounts = tools?.accounts ?? (account ? [account] : [])

  const run = useCallback(async (tool: CliTool, action: 'install' | 'update') => {
    const label = TOOL_META[tool].label
    setBusy(tool)
    output.openOutput(t(action === 'install' ? 'ai.tools.installing' : 'ai.tools.updating', { tool: label }))
    try {
      await output.runStream(`/ai/tools/${tool}/${action}-stream?user=${encodeURIComponent(account)}`)
      toast.success(t(action === 'install' ? 'ai.tools.installSuccess' : 'ai.tools.updateSuccess', { tool: label }))
      output.finishOutput()
      onChanged()
    } catch (err: unknown) {
      const message = streamErrorMessage(err, t('ai.errors.generic'))
      output.appendOutput('\n' + message + '\n')
      output.finishOutput()
      toast.error(message)
    } finally {
      setBusy(null)
    }
  }, [account, onChanged, output, t])

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="flex items-center gap-2">
        <span className="text-[12px] text-muted-foreground">{t('ai.account')}</span>
        <Select value={account} onValueChange={onAccountChange}>
          <SelectTrigger className="h-8 w-[10rem] rounded-xl text-[12px] font-mono" aria-label={t('ai.account')} title={t('ai.accountHint')} disabled={accounts.length === 0}>
            <SelectValue placeholder={t('ai.account')} />
          </SelectTrigger>
          <SelectContent>
            {accounts.map((a) => (
              <SelectItem key={a} value={a} className="font-mono text-[12px]">{a}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="w-px h-5 bg-border hidden sm:block" />
      {CLI_TOOLS.map((tool) => {
        const meta = TOOL_META[tool]
        const st = tools?.tools[tool]
        const checking = !tools
        return (
          <DropdownMenu key={tool}>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                className="flex items-center gap-1.5 h-8 px-2.5 rounded-xl border bg-card text-[12px] hover:bg-accent transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                aria-label={`${meta.label}: ${st?.installed ? st.version : t('ai.tools.notInstalled')}`}
              >
                <span className="h-5 w-5 rounded-md flex items-center justify-center text-[11px] font-bold"
                  style={{ backgroundColor: `${meta.color}1a`, color: meta.color }}>{meta.initial}</span>
                <span className="font-medium">{meta.label}</span>
                {checking ? (
                  <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" aria-hidden="true" />
                ) : st?.installed ? (
                  <>
                    <span className="font-mono text-muted-foreground">{st.version.match(/\d+\.\d+\.\d+/)?.[0] ?? st.version}</span>
                    {st.update_available && (
                      <span className="flex items-center gap-0.5 text-warning" title={t('ai.tools.updateAvailable', { version: st.latest })}>
                        <ArrowUpCircle className="h-3 w-3" aria-hidden="true" />{st.latest}
                      </span>
                    )}
                    {!st.logged_in && <KeyRound className="h-3 w-3 text-muted-foreground" aria-label={t('ai.tools.loginRequired')} />}
                  </>
                ) : (
                  <span className="text-muted-foreground">{t('ai.tools.notInstalled')}</span>
                )}
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-72 p-3 space-y-2">
              <DropdownMenuLabel className="p-0 text-[13px]">{meta.label}</DropdownMenuLabel>
              {st?.installed ? (
                <div className="space-y-1 text-[12px]">
                  <div className="flex items-center gap-1.5"><CheckCircle2 className="h-3.5 w-3.5 text-success" aria-hidden="true" /><span className="font-mono">{st.version}</span></div>
                  <div className="text-muted-foreground">{t('ai.tools.path')}: <span className="font-mono break-all">{st.path}</span></div>
                  <div className="text-muted-foreground">{t('ai.tools.latest')}: <span className="font-mono">{st.latest || '—'}</span></div>
                  <div className="text-muted-foreground">{st.logged_in ? t('ai.tools.loggedIn') : t('ai.tools.loginRequired')}</div>
                </div>
              ) : (
                <p className="text-[12px] text-muted-foreground">{t('ai.tools.notInstalled')}{st?.latest ? ` · ${t('ai.tools.latest')} ${st.latest}` : ''}</p>
              )}
              <DropdownMenuSeparator />
              {st?.installed ? (
                st.update_available ? (
                  <Button size="sm" className="rounded-xl w-full" disabled={busy !== null} onClick={() => run(tool, 'update')}>
                    {busy === tool ? <Loader2 className="animate-spin" aria-hidden="true" /> : <ArrowUpCircle aria-hidden="true" />}
                    {t('ai.tools.update')} {st.latest}
                  </Button>
                ) : (
                  <p className="text-[12px] text-muted-foreground">{t('ai.tools.upToDate')}</p>
                )
              ) : (
                <Button size="sm" className="rounded-xl w-full" disabled={busy !== null || checking} onClick={() => run(tool, 'install')}>
                  {busy === tool ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Download aria-hidden="true" />}
                  {t('ai.tools.install')}
                </Button>
              )}
              {/* Profiles, for the two tools whose configuration directory the
                  panel can point elsewhere. Without an account resolved yet
                  there is nothing to list and the confirmation text below
                  would have no name to put in it. */}
              {supportsProfiles(tool) && account !== '' && (
                <>
                  <DropdownMenuSeparator />
                  <ProfileSection tool={tool} account={account} onSessionsChanged={onSessionsChanged} />
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        )
      })}
    </div>
  )
}
