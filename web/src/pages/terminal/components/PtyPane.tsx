import type { PtyTab } from '@/lib/sessionRail'
import { TerminalSession } from '@/pages/terminal/components/TerminalSession'

// Every PTY tab stays mounted and only the active one is shown — the same
// contract as the old page: a hidden tab keeps its socket, which is the
// whole reason a reload within five minutes can pick it up again.
export function PtyPane({ tabs, activeId, fontSize }: { tabs: PtyTab[]; activeId: string | null; fontSize: number }) {
  return (
    <>
      {tabs.map((tab) => (
        <TerminalSession key={tab.id} sessionId={tab.id} active={activeId === tab.id} fontSize={fontSize} />
      ))}
    </>
  )
}
