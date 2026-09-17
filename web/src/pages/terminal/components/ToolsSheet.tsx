import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUpCircle, CheckCircle2, Download, KeyRound, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import type { AITools } from '@/types/api'
import { TOOL_META, supportsProfiles } from '@/lib/aiSessions'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { streamErrorMessage, type SSEOutput } from '@/components/OutputDialog'
import { ProfilePanel } from '@/pages/terminal/components/ProfilePanel'
import { TmuxBanner } from '@/pages/terminal/components/TmuxBanner'

type CliTool = 'claude' | 'codex' | 'gemini'
const CLI_TOOLS: CliTool[] = ['claude', 'codex', 'gemini']

export interface ToolsSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  tools: AITools | null
  /** GET /ai/tools failed; the panel says so and offers a retry */
  toolsError: boolean
  account: string
  onAccountChange: (account: string) => void
  onChanged: () => void
  onSessionsChanged: () => void
  output: SSEOutput
  onOpenTemporaryShell: () => void
}

/**
 * The 도구·계정 panel: which account the tools are probed for, tmux's state,
 * one card per CLI (version, update/install, login state, profiles), and at
 * the bottom the emergency door to the PTY engine. Install and update stream
 * into the shared OutputDialog exactly as the packages page does.
 */
export function ToolsSheet({ open, onOpenChange, tools, toolsError, account, onAccountChange, onChanged, onSessionsChanged, output, onOpenTemporaryShell }: ToolsSheetProps) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState<CliTool | null>(null)
  // Before GET /ai/tools resolves the account may be '' — Radix throws on an
  // empty SelectItem value, so an empty account is never an item.
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
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-md overflow-y-auto gap-0">
        <SheetHeader>
          <SheetTitle className="text-[15px]">{t('terminal.tools.title')}</SheetTitle>
          <SheetDescription className="text-[12px]">{t('terminal.tools.description')}</SheetDescription>
        </SheetHeader>
        <div className="px-4 pb-6 space-y-5">
          <div className="space-y-1.5">
            <Label htmlFor="tools-account">{t('ai.account')}</Label>
            <Select value={account} onValueChange={onAccountChange}>
              <SelectTrigger id="tools-account" className="font-mono text-[12px]" aria-label={t('ai.account')} disabled={accounts.length === 0}>
                <SelectValue placeholder={t('ai.account')} />
              </SelectTrigger>
              <SelectContent>
                {accounts.map((a) => <SelectItem key={a} value={a} className="font-mono text-[12px]">{a}</SelectItem>)}
              </SelectContent>
            </Select>
            <p className="text-[11px] text-muted-foreground">{t('ai.accountHint')}</p>
          </div>

          {toolsError && (
            <div className="flex items-center justify-between gap-3 rounded-2xl border border-destructive/30 bg-destructive/5 px-4 py-3">
              <p className="text-[12px]">{t('terminal.tools.loadFailed')}</p>
              <Button size="sm" variant="outline" className="rounded-xl" onClick={onChanged}>{t('terminal.tools.retry')}</Button>
            </div>
          )}

          <div className="space-y-1.5">
            <p className="text-[12px] font-medium">tmux</p>
            {tools === null ? (
              <p className="text-[12px] text-muted-foreground flex items-center gap-1.5"><Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />{t('ai.tools.checking')}</p>
            ) : tools.tmux.installed && tools.tmux.supported && tools.systemd_run ? (
              <p className="text-[12px] text-muted-foreground flex items-center gap-1.5"><CheckCircle2 className="h-3.5 w-3.5 text-success" aria-hidden="true" />{t('terminal.tools.tmuxOk', { version: tools.tmux.version })}</p>
            ) : (
              <TmuxBanner tools={tools} onChanged={onChanged} />
            )}
          </div>

          {CLI_TOOLS.map((tool) => {
            const meta = TOOL_META[tool]
            const st = tools?.tools[tool]
            const checking = !tools
            return (
              <div key={tool} className="rounded-2xl border border-border bg-card p-4 space-y-3">
                <div className="flex items-center gap-2">
                  <span className="h-6 w-6 rounded-md flex items-center justify-center text-[12px] font-bold" style={{ backgroundColor: `${meta.color}1a`, color: meta.color }} aria-hidden="true">{meta.initial}</span>
                  <span className="text-[13px] font-semibold">{meta.label}</span>
                  {checking ? <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" aria-label={t('ai.tools.checking')} />
                    : st?.installed ? (
                      <>
                        <span className="font-mono text-[12px] text-muted-foreground">{st.version.match(/\d+\.\d+\.\d+/)?.[0] ?? st.version}</span>
                        {st.update_available && <span className="flex items-center gap-0.5 text-[12px] text-warning" title={t('ai.tools.updateAvailable', { version: st.latest })}><ArrowUpCircle className="h-3 w-3" aria-hidden="true" />{st.latest}</span>}
                        {!st.logged_in && <KeyRound className="h-3 w-3 text-muted-foreground" aria-label={t('ai.tools.loginRequired')} />}
                      </>
                    ) : <span className="text-[12px] text-muted-foreground">{t('ai.tools.notInstalled')}</span>}
                </div>
                {st?.installed ? (
                  <div className="space-y-1 text-[12px] text-muted-foreground">
                    <div>{t('ai.tools.path')}: <span className="font-mono break-all">{st.path}</span></div>
                    <div>{t('ai.tools.latest')}: <span className="font-mono">{st.latest || '—'}</span></div>
                    <div>{st.logged_in ? t('ai.tools.loggedIn') : t('ai.tools.loginRequired')}</div>
                  </div>
                ) : (
                  <p className="text-[12px] text-muted-foreground">{t('ai.tools.notInstalled')}{st?.latest ? ` · ${t('ai.tools.latest')} ${st.latest}` : ''}</p>
                )}
                {st?.installed ? (
                  st.update_available ? (
                    <Button size="sm" className="rounded-xl w-full" disabled={busy !== null} onClick={() => run(tool, 'update')}>
                      {busy === tool ? <Loader2 className="animate-spin" aria-hidden="true" /> : <ArrowUpCircle aria-hidden="true" />}{t('ai.tools.update')} {st.latest}
                    </Button>
                  ) : <p className="text-[12px] text-muted-foreground">{t('ai.tools.upToDate')}</p>
                ) : (
                  <Button size="sm" className="rounded-xl w-full" disabled={busy !== null || checking} onClick={() => run(tool, 'install')}>
                    {busy === tool ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Download aria-hidden="true" />}{t('ai.tools.install')}
                  </Button>
                )}
                {/* Profiles need a tool that supports them, a resolved account,
                    and the tool actually installed for that account (see the
                    ProfilePanel comment). The panel mounts with the sheet, so
                    the list is fetched when the operator opens it. */}
                {supportsProfiles(tool) && account !== '' && st?.installed === true && (
                  <div className="border-t border-border pt-3">
                    <ProfilePanel tool={tool} account={account} onSessionsChanged={onSessionsChanged} />
                  </div>
                )}
              </div>
            )
          })}

          {/* The emergency door: a PTY shell as the panel account, outside
              tmux, for the day tmux is present but something around it is
              broken. Quiet on purpose — it is not a peer of 새 세션. */}
          <div className="pt-2 border-t border-border">
            <button type="button" className="text-[12px] text-primary hover:underline" onClick={() => { onOpenChange(false); onOpenTemporaryShell() }}>
              {t('terminal.tools.temporaryShell')}
            </button>
            <p className="text-[11px] text-muted-foreground mt-0.5">{t('terminal.tools.temporaryShellHint')}</p>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
