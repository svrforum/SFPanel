import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { AISession, AITools } from '@/types/api'
import { aiErrorMessage, formatTimestamp, titlePrefix, waitingCount } from '@/lib/aiSessions'
import { OutputDialog, useSSEOutput } from '@/components/OutputDialog'
import { useConfirm } from '@/components/ConfirmDialog'
import MobileTerminalBar from '@/components/MobileTerminalBar'
import type { TerminalSessionElement } from '@/pages/terminal/components/TerminalSession'
import { ToolChips } from '@/pages/ai/components/ToolChips'
import { TmuxBanner } from '@/pages/ai/components/TmuxBanner'
import { SessionTabs } from '@/pages/ai/components/SessionTabs'
import { SessionPane } from '@/pages/ai/components/SessionPane'
import { NewSessionDialog } from '@/pages/ai/components/NewSessionDialog'
import { useAISessions } from '@/pages/ai/hooks/useAISessions'

const nodeSuffix = () => api.currentNode || 'local'
const accountKey = () => `sfpanel_ai_account:${nodeSuffix()}`
const activeKey = () => `sfpanel_ai_active:${nodeSuffix()}`
// The terminal page's font size applies here too; there is no second control.
const FONT_SIZE_KEY = 'sfpanel_terminal_fontsize'

function readLS(key: string): string {
  try { return localStorage.getItem(key) || '' } catch { return '' }
}
function writeLS(key: string, value: string) {
  try { localStorage.setItem(key, value) } catch { /* private mode */ }
}

export default function AI() {
  const { t } = useTranslation()
  const output = useSSEOutput()
  const confirm = useConfirm()
  const [account, setAccount] = useState<string>(() => readLS(accountKey()))
  const [tools, setTools] = useState<AITools | null>(null)
  const [activeId, setActiveId] = useState<string | null>(() => readLS(activeKey()) || null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const fontSize = useMemo(() => {
    const n = parseInt(readLS(FONT_SIZE_KEY), 10)
    return Number.isFinite(n) && n >= 10 && n <= 24 ? n : 14
  }, [])
  const { sessions, loaded, refresh } = useAISessions()

  // Promise callbacks rather than await: the effect below kicks this off
  // synchronously on mount, and an async body would trip
  // react-hooks/set-state-in-effect (same reason as ClusterNodes/Dashboard).
  //
  // First load asks with an empty user, which the server answers for its own
  // account and names in `account`; adopting that name must not send the
  // identical request a second time.
  const loadTools = useCallback(() => {
    api.getAITools(account).then((data) => {
      setTools(data)
      if (!account && data.account !== data.panel_account) setAccount(data.panel_account)
    }).catch(() => {
      // Header degrades to "checking"; the session list is independent.
    })
  }, [account])
  useEffect(() => { loadTools() }, [loadTools])
  useEffect(() => { if (account) writeLS(accountKey(), account) }, [account])

  // Keep the active tab valid: fall back to the first session, remember it per node.
  useEffect(() => {
    if (!loaded) return
    // The realignment only fires when activeId and the server's list actually
    // disagree, so the cascading-render risk the rule guards is bounded
    // (same shape as the Terminal page's active-tab fixup).
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (sessions.length === 0) { setActiveId(null); return }
    if (!activeId || !sessions.some((s) => s.id === activeId)) setActiveId(sessions[0].id)
  }, [sessions, loaded, activeId])
  useEffect(() => { if (activeId) writeLS(activeKey(), activeId) }, [activeId])

  // "(n) SFPanel" while something waits in a background tab.
  useEffect(() => {
    const base = document.title.replace(/^\(\d+\) /, '')
    document.title = titlePrefix(waitingCount(sessions, activeId)) + base
    return () => { document.title = base }
  }, [sessions, activeId])

  const active = sessions.find((s) => s.id === activeId) ?? null

  const act = useCallback(async (fn: () => Promise<unknown>) => {
    try { await fn() } catch (err: unknown) { toast.error(aiErrorMessage(err, t)) }
    await refresh()
  }, [refresh, t])

  const rename = (id: string, title: string) => act(() => api.renameAISession(id, title))
  const rerun = (s: AISession) => act(() => api.rerunAISession(s.id))
  const restart = (s: AISession) => act(() => api.restartAISession(s.id))
  const removeEnded = (s: AISession) => act(() => api.deleteAISession(s.id))
  const kill = async (s: AISession) => {
    if (!(await confirm({ title: t('ai.tabs.closeConfirmTitle'), description: t('ai.tabs.closeConfirmDesc', { title: s.title }), danger: true, confirmLabel: t('ai.tabs.close') }))) return
    await act(() => api.deleteAISession(s.id))
  }
  const info = (s: AISession) => {
    toast.info(`${t('ai.tabs.infoAccount')}: ${s.run_as} · ${t('ai.tabs.infoDir')}: ${s.cwd} · ${t('ai.tabs.infoCreated')}: ${formatTimestamp(s.created_at)}`)
  }

  const sendKey = useCallback((data: string) => {
    const el = document.querySelector<TerminalSessionElement>('[data-terminal-session="active"]')
    if (el?.__wsRef?.current && el.__wsRef.current.readyState === WebSocket.OPEN) {
      el.__wsRef.current.send(new TextEncoder().encode(data))
    }
    el?.__termRef?.current?.focus()
  }, [])

  return (
    <div className="flex flex-col h-full gap-3 overflow-hidden">
      <div className="flex flex-wrap items-start justify-between gap-3 shrink-0">
        <div>
          <h1 className="text-[22px] font-bold tracking-tight">{t('ai.title')}</h1>
          <p className="text-[13px] text-muted-foreground mt-1">{t('ai.subtitle')}</p>
        </div>
        <ToolChips tools={tools} account={account || tools?.panel_account || ''} onAccountChange={(a) => { setTools(null); setAccount(a) }} onChanged={loadTools} output={output} />
      </div>
      <TmuxBanner tools={tools} onChanged={loadTools} />
      <div className="flex flex-col flex-1 min-h-0 rounded-2xl overflow-hidden border border-border bg-card">
        <SessionTabs sessions={sessions} activeId={activeId} onSelect={setActiveId} onNew={() => setDialogOpen(true)}
          onRename={rename} onRerun={rerun} onRestart={restart} onKill={kill} onRemoveEnded={removeEnded} onInfo={info} />
        <div className="flex-1 min-h-0 relative">
          <SessionPane session={active} fontSize={fontSize} onNew={() => setDialogOpen(true)} onRestart={restart} onRemoveEnded={removeEnded} />
        </div>
        <MobileTerminalBar onSendKey={sendKey} />
      </div>
      {/* Focus the new tab only once the list that contains it has landed:
          setting activeId first would leave it pointing at an id the
          realignment effect above cannot find, and it would snap back to
          sessions[0] before the refresh arrived. */}
      <NewSessionDialog open={dialogOpen} onOpenChange={setDialogOpen} account={account || tools?.panel_account || ''} tools={tools}
        onCreated={(s) => { void refresh().then(() => setActiveId(s.id)) }} />
      <OutputDialog state={output.state} onClose={output.closeOutput} />
    </div>
  )
}
