import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUpCircle, CheckCircle2, Download, KeyRound, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import type { AITools } from '@/types/api'
import { TOOL_META, supportsProfiles } from '@/lib/aiSessions'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { streamErrorMessage, type SSEOutput } from '@/components/OutputDialog'
import { ProfilePanel } from '@/pages/ai/components/ProfilePanel'

type CliTool = 'claude' | 'codex' | 'gemini'
const CLI_TOOLS: CliTool[] = ['claude', 'codex', 'gemini']

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
                  panel can point elsewhere. Three things have to hold: the
                  tool supports profiles, an account is resolved (otherwise
                  there is nothing to list and the confirmation text would have
                  no name to put in it), and the tool is actually installed for
                  that account — a create field next to this chip's own Install
                  button would make a login directory for a CLI that cannot
                  use it. */}
              {supportsProfiles(tool) && account !== '' && st?.installed === true && (
                <>
                  <DropdownMenuSeparator />
                  <ProfilePanel tool={tool} account={account} onSessionsChanged={onSessionsChanged} />
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        )
      })}
    </div>
  )
}
