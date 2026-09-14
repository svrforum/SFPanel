import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import type { AITools } from '@/types/api'
import { OutputDialog, useSSEOutput } from '@/components/OutputDialog'
import { ToolChips } from '@/pages/ai/components/ToolChips'
import { TmuxBanner } from '@/pages/ai/components/TmuxBanner'

const nodeSuffix = () => api.currentNode || 'local'
const accountKey = () => `sfpanel_ai_account:${nodeSuffix()}`

export default function AI() {
  const { t } = useTranslation()
  const output = useSSEOutput()
  const [account, setAccount] = useState<string>(() => {
    try { return localStorage.getItem(accountKey()) || '' } catch { return '' }
  })
  const [tools, setTools] = useState<AITools | null>(null)

  // Promise callbacks rather than await: the effect below kicks this off
  // synchronously on mount, and an async body would trip
  // react-hooks/set-state-in-effect (same reason as ClusterNodes/Dashboard).
  const loadTools = useCallback(() => {
    api.getAITools(account).then((data) => {
      setTools(data)
      if (!account) setAccount(data.panel_account)
    }).catch(() => {
      // The header degrades to "checking"; the tab list below does not depend on it.
    })
  }, [account])

  useEffect(() => { loadTools() }, [loadTools])
  useEffect(() => {
    try { if (account) localStorage.setItem(accountKey(), account) } catch { /* private mode */ }
  }, [account])

  return (
    <div className="flex flex-col h-full gap-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-[22px] font-bold tracking-tight">{t('ai.title')}</h1>
          <p className="text-[13px] text-muted-foreground mt-1">{t('ai.subtitle')}</p>
        </div>
        <ToolChips tools={tools} account={account || tools?.panel_account || ''} onAccountChange={(a) => { setTools(null); setAccount(a) }} onChanged={loadTools} output={output} />
      </div>
      <TmuxBanner tools={tools} onChanged={loadTools} />
      {/* Task 12 mounts the session tabs and pane here */}
      <OutputDialog state={output.state} onClose={output.closeOutput} />
    </div>
  )
}
