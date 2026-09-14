import { useTranslation } from 'react-i18next'
import { Plus, RotateCcw, Sparkles, X } from 'lucide-react'
import type { AISession } from '@/types/api'
import { Button } from '@/components/ui/button'
import { TerminalSession } from '@/pages/terminal/components/TerminalSession'

// Only the active session is mounted, so only one tmux client exists per
// open page and background tabs keep their bell (spec §4). Switching tabs
// remounts: the server replays the pane history first, so it looks instant.
export function SessionPane({
  session,
  fontSize,
  onNew,
  onRestart,
  onRemoveEnded,
}: {
  session: AISession | null
  fontSize: number
  onNew: () => void
  onRestart: (s: AISession) => void
  onRemoveEnded: (s: AISession) => void
}) {
  const { t } = useTranslation()
  if (!session) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground">
        <div className="text-center max-w-sm px-4">
          <Sparkles className="h-12 w-12 mx-auto mb-3 opacity-50" aria-hidden="true" />
          <p className="font-medium">{t('ai.tabs.noSessions')}</p>
          <p className="text-[12px] mt-1">{t('ai.tabs.noSessionsHint')}</p>
          <Button variant="outline" size="sm" className="mt-3 rounded-xl" onClick={onNew}>
            <Plus className="h-4 w-4 mr-1" aria-hidden="true" />{t('ai.tabs.new')}
          </Button>
        </div>
      </div>
    )
  }
  if (session.state === 'ended') {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground">
        <div className="text-center max-w-sm px-4">
          <p className="font-medium text-foreground">{t('ai.tabs.ended')}</p>
          <p className="text-[12px] mt-1">{t('ai.tabs.endedHint')}</p>
          <div className="flex justify-center gap-2 mt-3">
            <Button size="sm" className="rounded-xl" onClick={() => onRestart(session)}>
              <RotateCcw className="h-4 w-4 mr-1" aria-hidden="true" />{t('ai.tabs.restart')}
            </Button>
            <Button size="sm" variant="outline" className="rounded-xl" onClick={() => onRemoveEnded(session)}>
              <X className="h-4 w-4 mr-1" aria-hidden="true" />{t('ai.tabs.removeEnded')}
            </Button>
          </div>
        </div>
      </div>
    )
  }
  return (
    <TerminalSession
      key={session.id}
      sessionId={session.id}
      active
      fontSize={fontSize}
      wsPath="/ws/ai/attach"
      wsParams={{ session_id: session.id }}
    />
  )
}
