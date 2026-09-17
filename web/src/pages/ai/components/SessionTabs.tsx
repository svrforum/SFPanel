import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, HelpCircle, Plus, ShieldAlert } from 'lucide-react'
import type { AISession } from '@/types/api'
import { TOOL_META, dangerousFlagFor, stateDotClass } from '@/lib/aiSessions'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger,
} from '@/components/ui/context-menu'

// One tab per server-side session, in creation order (the server sorts).
// Right-click / long-press opens the actions; double-click renames inline,
// the same gesture the Terminal page uses.
export function SessionTabs({
  sessions,
  activeId,
  onSelect,
  onNew,
  onRename,
  onRerun,
  onRestart,
  onKill,
  onRemoveEnded,
  onInfo,
}: {
  sessions: AISession[]
  activeId: string | null
  onSelect: (id: string) => void
  onNew: () => void
  onRename: (id: string, title: string) => void
  onRerun: (s: AISession) => void
  onRestart: (s: AISession) => void
  onKill: (s: AISession) => void
  onRemoveEnded: (s: AISession) => void
  onInfo: (s: AISession) => void
}) {
  const { t } = useTranslation()
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editingName, setEditingName] = useState('')
  const editRef = useRef<HTMLInputElement>(null)
  useEffect(() => { if (editingId) editRef.current?.select() }, [editingId])

  const commitRename = (id: string) => {
    const name = editingName.trim()
    setEditingId(null)
    if (name) onRename(id, name)
  }

  return (
    <div className="flex items-center bg-card border-b border-border px-2 shrink-0">
      <div className="flex items-center gap-0.5 overflow-x-auto py-1 flex-1" role="tablist">
        {sessions.map((s) => {
          const meta = TOOL_META[s.tool] ?? TOOL_META.shell
          const active = s.id === activeId
          // The bypass marker's one label, on the tooltip and on the
          // accessible name both. It names the flag as the CLI spells it,
          // because that is the word an operator can look up.
          const dangerLabel = t('ai.tabs.dangerousLaunch', { flag: dangerousFlagFor(s.tool) })
          return (
            <ContextMenu key={s.id}>
              <ContextMenuTrigger asChild>
                <div
                  role="tab"
                  aria-selected={active}
                  tabIndex={0}
                  title={`${s.title} · ${t('ai.state.' + s.state)}`}
                  className={cn(
                    'flex items-center gap-1.5 px-3 py-1.5 rounded-t text-xs cursor-pointer select-none shrink-0 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring/40',
                    active ? 'bg-secondary text-foreground' : 'text-muted-foreground hover:text-foreground hover:bg-accent',
                    s.state === 'ended' && 'opacity-60'
                  )}
                  onClick={() => onSelect(s.id)}
                  onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect(s.id) } }}
                  onDoubleClick={() => { setEditingId(s.id); setEditingName(s.title) }}
                >
                  <span className="h-4 w-4 rounded flex items-center justify-center text-[10px] font-bold shrink-0"
                    style={{ backgroundColor: `${meta.color}1a`, color: meta.color }} aria-hidden="true">{meta.initial}</span>
                  {editingId === s.id ? (
                    <input
                      ref={editRef}
                      value={editingName}
                      onChange={(e) => setEditingName(e.target.value)}
                      onBlur={() => commitRename(s.id)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') commitRename(s.id)
                        if (e.key === 'Escape') setEditingId(null)
                        e.stopPropagation()
                      }}
                      onClick={(e) => e.stopPropagation()}
                      className="bg-transparent border-b border-primary outline-none text-foreground w-28 text-xs"
                      maxLength={64}
                      autoFocus
                    />
                  ) : (
                    <span className="max-w-[18ch] truncate">{s.title}</span>
                  )}
                  {/* Which login the session runs under. Only a non-default
                      profile has a name; two tabs on the same tool and
                      directory are otherwise identical on screen while
                      talking to different accounts. */}
                  {s.profile && editingId !== s.id && (
                    <span className="px-1 rounded bg-muted text-[10px] font-mono max-w-[10ch] truncate shrink-0"
                      title={`${t('ai.profiles.label')}: ${s.profile}`}>{s.profile}</span>
                  )}
                  <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', stateDotClass(s.state, active))}
                    role="status" aria-label={t('ai.state.' + s.state)} />
                  {s.persistence === 'process' && s.state !== 'ended' && (
                    <AlertTriangle className="h-3 w-3 text-warning shrink-0" aria-label={t('ai.tabs.processMode')} />
                  )}
                  {/* Started with the CLI's own bypass flag: this session
                      edits files and runs commands without asking once, and
                      the dialog that chose that is long gone. Same marker
                      vocabulary as the process-mode warning above — one icon
                      carrying the sentence — and gone once the session has
                      ended, because nothing is running under it any more.
                      The tooltip sits on the wrapping span, the way the
                      Terminal page's root badge does it: a `title` attribute
                      on an <svg> is not the tooltip mechanism, and the tab's
                      own title would otherwise be all hover says. The label
                      lives on that one span — role="img" plus aria-label, with
                      the icon aria-hidden — because naming both the span (by
                      its title) and the icon (by its aria-label) made a screen
                      reader announce the same sentence twice. */}
                  {s.launch?.dangerous && s.state !== 'ended' && (
                    <span className="shrink-0 flex items-center" role="img" aria-label={dangerLabel} title={dangerLabel}>
                      <ShieldAlert className="h-3 w-3 text-destructive" aria-hidden="true" />
                    </span>
                  )}
                  {s.unknown && <HelpCircle className="h-3 w-3 shrink-0" aria-label={t('ai.tabs.unknown')} />}
                </div>
              </ContextMenuTrigger>
              <ContextMenuContent>
                <ContextMenuItem onSelect={() => { setEditingId(s.id); setEditingName(s.title) }} disabled={s.unknown}>{t('ai.tabs.rename')}</ContextMenuItem>
                <ContextMenuItem onSelect={() => onInfo(s)}>{t('ai.tabs.info')}</ContextMenuItem>
                <ContextMenuSeparator />
                {s.state === 'shell' && s.tool !== 'shell' && !s.unknown && (
                  <ContextMenuItem onSelect={() => onRerun(s)}>{t('ai.tabs.rerun')}</ContextMenuItem>
                )}
                {s.state === 'ended' ? (
                  <>
                    <ContextMenuItem onSelect={() => onRestart(s)}>{t('ai.tabs.restart')}</ContextMenuItem>
                    <ContextMenuItem onSelect={() => onRemoveEnded(s)}>{t('ai.tabs.removeEnded')}</ContextMenuItem>
                  </>
                ) : (
                  <ContextMenuItem className="text-destructive" onSelect={() => onKill(s)}>{t('ai.tabs.close')}</ContextMenuItem>
                )}
              </ContextMenuContent>
            </ContextMenu>
          )
        })}
      </div>
      <Button variant="ghost" size="sm" className="h-6 w-6 p-0 ml-2 shrink-0 text-muted-foreground hover:text-foreground hover:bg-accent"
        onClick={onNew} title={t('ai.tabs.new')} aria-label={t('ai.tabs.new')}>
        <Plus className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}
