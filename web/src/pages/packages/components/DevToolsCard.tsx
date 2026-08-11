import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CheckCircle2, Package, Settings2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { streamErrorMessage, type SSEOutput } from '@/components/OutputDialog'
import { DevToolCard } from '@/pages/packages/components/DevToolCard'
import { NodeVersionDialog } from '@/pages/packages/components/NodeVersionDialog'

export interface DevToolStatus {
  installed: boolean
  version: string
  [key: string]: unknown
}

type ToolId = 'node' | 'claude' | 'codex' | 'gemini'

const STATUS_CALLS: Record<ToolId, () => Promise<DevToolStatus>> = {
  node: () => api.getNodeStatus(),
  claude: () => api.getClaudeStatus(),
  codex: () => api.getCodexStatus(),
  gemini: () => api.getGeminiStatus(),
}

const TOOLS: {
  id: ToolId
  title: string
  subtitle: string
  color: string
  initial: string
  path: string
  installingKey: string
  successKey: string
  installKey: string
  requiresNode?: boolean
}[] = [
  { id: 'node', title: 'Node.js', subtitle: 'NVM + LTS', color: '#68a063', initial: 'N', path: '/packages/install-node', installingKey: 'packages.installingNode', successKey: 'packages.nodeInstallSuccess', installKey: 'packages.installNode' },
  { id: 'claude', title: 'Claude Code', subtitle: 'Anthropic CLI', color: '#d97757', initial: 'C', path: '/packages/install-claude', installingKey: 'packages.installingClaude', successKey: 'packages.claudeInstallSuccess', installKey: 'packages.installClaude' },
  { id: 'codex', title: 'Codex', subtitle: 'OpenAI CLI', color: '#10a37f', initial: 'X', path: '/packages/install-codex', installingKey: 'packages.installingCodex', successKey: 'packages.codexInstallSuccess', installKey: 'packages.installCodex', requiresNode: true },
  { id: 'gemini', title: 'Gemini CLI', subtitle: 'Google CLI', color: '#4285f4', initial: 'G', path: '/packages/install-gemini', installingKey: 'packages.installingGemini', successKey: 'packages.geminiInstallSuccess', installKey: 'packages.installGemini', requiresNode: true },
]

// Dev tools grid (Node/Claude/Codex/Gemini) + the NVM version dialog. Installs
// stream into the shared output dialog via postTextStream (keeps ?node=).
export function DevToolsCard({ output }: { output: SSEOutput }) {
  const { t } = useTranslation()
  const [statuses, setStatuses] = useState<Record<ToolId, DevToolStatus | null>>({
    node: null,
    claude: null,
    codex: null,
    gemini: null,
  })
  const [checking, setChecking] = useState<Record<ToolId, boolean>>({
    node: false,
    claude: false,
    codex: false,
    gemini: false,
  })
  const [installing, setInstalling] = useState<ToolId | null>(null)
  const [nodeVersionDialog, setNodeVersionDialog] = useState(false)

  const fetchStatus = useCallback(async (id: ToolId) => {
    setChecking((prev) => ({ ...prev, [id]: true }))
    try {
      const data = await STATUS_CALLS[id]()
      setStatuses((prev) => ({ ...prev, [id]: data }))
    } catch {
      // silent — the card just keeps showing "not installed"
    } finally {
      setChecking((prev) => ({ ...prev, [id]: false }))
    }
  }, [])

  useEffect(() => {
    TOOLS.forEach((tool) => fetchStatus(tool.id))
  }, [fetchStatus])

  const handleInstall = useCallback(async (id: ToolId) => {
    const tool = TOOLS.find((td) => td.id === id)
    if (!tool) return
    setInstalling(id)
    output.openOutput(t(tool.installingKey))
    try {
      await output.runStream(tool.path)
      toast.success(t(tool.successKey))
      output.finishOutput()
      await fetchStatus(id)
    } catch (err: unknown) {
      const message = streamErrorMessage(err, t('packages.streamStartFailed'))
      output.appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
      output.finishOutput()
      toast.error(message)
    } finally {
      setInstalling(null)
    }
  }, [output, fetchStatus, t])

  const nodeStatus = statuses.node
  const npmVersion = (nodeStatus as (DevToolStatus & { npm_version?: string }) | null)?.npm_version

  // Node.js installed state carries extras: npm version + the manage button.
  const nodeInstalledExtra = (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-1.5">
            <CheckCircle2 className="h-3.5 w-3.5 text-success" aria-hidden="true" />
            <span className="text-[12px] font-medium font-mono">{nodeStatus?.version}</span>
          </div>
          {npmVersion && (
            <p className="text-[11px] text-muted-foreground">npm {npmVersion}</p>
          )}
        </div>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 w-7 p-0"
          onClick={() => setNodeVersionDialog(true)}
          title={t('packages.nodeVersionManage')}
          aria-label={t('packages.nodeVersionManage')}
        >
          <Settings2 className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  )

  return (
    <div className="bg-card rounded-2xl card-shadow">
      <div className="px-6 pt-5 pb-4">
        <h3 className="text-[15px] font-semibold flex items-center gap-2">
          <Package className="h-4 w-4" aria-hidden="true" />
          {t('packages.devTools')}
        </h3>
        <p className="text-[13px] text-muted-foreground mt-1">
          {t('packages.devToolsDescription')}
        </p>
      </div>
      <div className="px-6 pb-5 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        {TOOLS.map((tool) => {
          const status = statuses[tool.id]
          const nodeMissing = !!tool.requiresNode && !nodeStatus?.installed
          return (
            <DevToolCard
              key={tool.id}
              title={tool.title}
              subtitle={tool.subtitle}
              accentColor={tool.color}
              initial={tool.initial}
              checking={checking[tool.id]}
              installed={!!status?.installed}
              version={status?.version}
              installLabel={t(tool.installKey)}
              installing={installing === tool.id}
              onInstall={() => handleInstall(tool.id)}
              installDisabled={nodeMissing}
              footnote={nodeMissing ? t('packages.nodeRequired') : undefined}
              installedExtra={tool.id === 'node' ? nodeInstalledExtra : undefined}
            />
          )
        })}
      </div>

      <NodeVersionDialog
        open={nodeVersionDialog}
        onOpenChange={setNodeVersionDialog}
        nodeStatus={nodeStatus}
        nvmInstalling={installing === 'node'}
        onInstallNvm={() => handleInstall('node')}
        onNodeChanged={() => fetchStatus('node')}
        output={output}
      />
    </div>
  )
}
