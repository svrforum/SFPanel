import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, ChevronsLeft, ChevronsRight, HelpCircle, History, Plus, ShieldAlert, Wrench } from 'lucide-react'
import type { TerminalSession as TerminalSessionInfo } from '@/types/api'
import { TOOL_META, dangerousFlagFor, stateDotClass } from '@/lib/aiSessions'
import { activeKey, railNote, type RailGroup, type RailItem } from '@/lib/sessionRail'
import { cn } from '@/lib/utils'
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from '@/components/ui/context-menu'

export type RailAction = 'info' | 'rerun' | 'restart' | 'removeEnded' | 'kill' | 'closeTab'

export interface SessionRailProps {
  groups: RailGroup[]
  active: string | null
  collapsed: boolean
  fallback: boolean
  reattachable: TerminalSessionInfo[]
  onSelect: (key: string) => void
  onNew: () => void
  onReattach: (sessionId: string) => void
  onOpenTools: () => void
  /** absent inside the mobile drawer, which has nothing to collapse */
  onToggleCollapsed?: () => void
  onRename: (item: RailItem, title: string) => void
  onAction: (action: RailAction, item: RailItem) => void
}

const ROW = 'relative flex items-center gap-2 h-10 w-full text-left select-none outline-none cursor-pointer transition-colors focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40'

/**
 * The left rail: one primary action, the sessions grouped by directory, the
 * temporary (PTY) group last, and the tools entry. It is a vertical tablist —
 * one row is showing in the pane — so arrow keys move between rows and the
 * Android app can find the active row by [role=tab][aria-selected=true].
 */
export function SessionRail({ groups, active, collapsed, fallback, reattachable, onSelect, onNew, onReattach, onOpenTools, onToggleCollapsed, onRename, onAction }: SessionRailProps) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState<string | null>(null)
  const [editingName, setEditingName] = useState('')
  const editRef = useRef<HTMLInputElement>(null)
  useEffect(() => { if (editing) editRef.current?.select() }, [editing])

  const startRename = (item: RailItem) => {
    setEditing(activeKey(item))
    setEditingName(item.kind === 'tmux' ? item.session.title : item.tab.title)
  }
  const commitRename = (item: RailItem) => {
    const name = editingName.trim()
    setEditing(null)
    if (name) onRename(item, name)
  }

  // Roving focus over every row, across groups. The rename input stops
  // propagation of its own keys, so editing a name never moves focus.
  const onListKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) return
    const rows = Array.from(e.currentTarget.querySelectorAll<HTMLElement>('[role="tab"]'))
    if (rows.length === 0) return
    const i = rows.indexOf(document.activeElement as HTMLElement)
    const next = e.key === 'Home' ? 0
      : e.key === 'End' ? rows.length - 1
      : e.key === 'ArrowDown' ? Math.min(i + 1, rows.length - 1)
      : Math.max(i - 1, 0)
    e.preventDefault()
    rows[next].focus()
  }

  const newLabel = fallback ? t('terminal.rail.newTemporary') : t('terminal.rail.newSession')

  return (
    <div className="flex flex-col h-full min-h-0 text-console-foreground">
      <div className={cn('shrink-0 pt-3 pb-2', collapsed ? 'px-2' : 'px-3')}>
        <button type="button" onClick={onNew} title={newLabel} aria-label={newLabel}
          className={cn('flex items-center justify-center gap-1.5 h-9 w-full rounded-xl bg-primary text-primary-foreground text-[13px] font-medium hover:bg-primary/90 outline-none focus-visible:ring-2 focus-visible:ring-ring/40')}>
          <Plus className="h-4 w-4" aria-hidden="true" />
          {!collapsed && newLabel}
        </button>
      </div>

      <div role="tablist" aria-orientation="vertical" aria-label={t('terminal.rail.sessions')} onKeyDown={onListKeyDown}
        className="flex-1 min-h-0 overflow-y-auto no-scrollbar pb-2">
        {groups.map((g) => (
          <section key={g.key} aria-label={g.label}>
            <div className={cn('pt-3 pb-1', collapsed ? 'px-2' : 'px-3')}>
              {collapsed ? (
                <div className="h-px bg-console-border" aria-hidden="true" />
              ) : (
                <>
                  <div className="text-[13px] font-semibold truncate">{g.label}</div>
                  {g.path && <div className="text-[11px] font-mono text-console-muted truncate" title={g.path}>{g.path}</div>}
                  {g.temporary && <div className="text-[11px] text-console-muted">{t('terminal.rail.temporaryHint')}</div>}
                </>
              )}
            </div>

            {g.items.map((item) => {
              const key = activeKey(item)
              const isActive = key === active
              const s = item.kind === 'tmux' ? item.session : null
              const meta = s ? (TOOL_META[s.tool] ?? TOOL_META.shell) : TOOL_META.shell
              // Narrow on item.kind, not on s: TypeScript does not carry the
              // null check on s back to item.
              const title = item.kind === 'tmux' ? item.session.title : item.tab.title
              const note = s ? railNote(s, isActive) : null
              const noteText = note === 'waiting' ? t('terminal.rail.waiting') : note === 'toolExited' ? t('terminal.rail.toolExited') : note === 'ended' ? t('ai.state.ended') : ''
              const dangerLabel = s ? t('ai.tabs.dangerousLaunch', { flag: dangerousFlagFor(s.tool) }) : ''
              return (
                <ContextMenu key={key}>
                  <ContextMenuTrigger asChild>
                    <div role="tab" aria-selected={isActive} tabIndex={isActive ? 0 : -1} data-rail-key={key}
                      title={collapsed ? `${title}${noteText ? ' · ' + noteText : ''}` : undefined}
                      className={cn(ROW, collapsed ? 'justify-center px-0' : 'px-3',
                        isActive ? 'bg-console-foreground/10' : 'hover:bg-console-foreground/5',
                        s?.state === 'ended' && 'opacity-50')}
                      onClick={() => onSelect(key)}
                      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect(key) } }}
                      onDoubleClick={() => { if (!s?.unknown) startRename(item) }}>
                      {isActive && <span aria-hidden="true" className="absolute left-0 top-2 bottom-2 w-0.5 rounded-full" style={{ backgroundColor: meta.color }} />}
                      {/* The dot is a picture of the state, not a live region:
                          role="status" makes every screen reader announce the
                          whole rail again whenever any row's state changes. */}
                      <span className={cn('h-1.5 w-1.5 rounded-full shrink-0 motion-reduce:animate-none', s ? stateDotClass(s.state, isActive) : 'bg-console-muted')}
                        role="img" aria-label={s ? t('ai.state.' + s.state) : t('terminal.rail.temporarySession')} />
                      <span className="h-[18px] w-[18px] rounded-md flex items-center justify-center text-[10px] font-bold shrink-0"
                        style={{ backgroundColor: `${meta.color}1a`, color: meta.color }} aria-hidden="true">{meta.initial}</span>
                      {!collapsed && (
                        <div className="min-w-0 flex-1">
                          {editing === key ? (
                            <input ref={editRef} value={editingName} onChange={(e) => setEditingName(e.target.value)}
                              onBlur={() => commitRename(item)}
                              onKeyDown={(e) => { if (e.key === 'Enter') commitRename(item); if (e.key === 'Escape') setEditing(null); e.stopPropagation() }}
                              onClick={(e) => e.stopPropagation()}
                              /* double-click selects a word in the field; the row would
                                 read it as "rename" and restart the edit, losing the text */
                              onDoubleClick={(e) => e.stopPropagation()}
                              className="w-full bg-transparent border-b border-primary outline-none text-[13px] text-console-foreground" maxLength={64} autoFocus />
                          ) : (
                            <div className="flex items-center gap-1.5 min-w-0">
                              <span className="text-[13px] font-medium truncate">{title}</span>
                              {s?.profile && (
                                <span className="px-1 rounded bg-console-foreground/10 text-[10px] font-mono max-w-[10ch] truncate shrink-0"
                                  title={`${t('ai.profiles.label')}: ${s.profile}`}>{s.profile}</span>
                              )}
                              {s?.persistence === 'process' && s.state !== 'ended' && (
                                <AlertTriangle className="h-3 w-3 text-warning shrink-0" aria-label={t('ai.tabs.processMode')} />
                              )}
                              {s?.launch?.dangerous && s.state !== 'ended' && (
                                <span className="shrink-0 flex items-center" role="img" aria-label={dangerLabel} title={dangerLabel}>
                                  <ShieldAlert className="h-3 w-3 text-destructive" aria-hidden="true" />
                                </span>
                              )}
                              {s?.unknown && <HelpCircle className="h-3 w-3 shrink-0" aria-label={t('ai.tabs.unknown')} />}
                            </div>
                          )}
                          {noteText && editing !== key && <div className="text-[11px] text-console-muted truncate">{noteText}</div>}
                        </div>
                      )}
                    </div>
                  </ContextMenuTrigger>
                  <ContextMenuContent>
                    {s ? (
                      <>
                        <ContextMenuItem onSelect={() => startRename(item)} disabled={s.unknown}>{t('ai.tabs.rename')}</ContextMenuItem>
                        <ContextMenuItem onSelect={() => onAction('info', item)}>{t('ai.tabs.info')}</ContextMenuItem>
                        <ContextMenuSeparator />
                        {s.state === 'shell' && s.tool !== 'shell' && !s.unknown && (
                          <ContextMenuItem onSelect={() => onAction('rerun', item)}>{t('ai.tabs.rerun')}</ContextMenuItem>
                        )}
                        {s.state === 'ended' ? (
                          <>
                            <ContextMenuItem onSelect={() => onAction('restart', item)}>{t('ai.tabs.restart')}</ContextMenuItem>
                            <ContextMenuItem onSelect={() => onAction('removeEnded', item)}>{t('ai.tabs.removeEnded')}</ContextMenuItem>
                          </>
                        ) : (
                          <ContextMenuItem className="text-destructive" onSelect={() => onAction('kill', item)}>{t('ai.tabs.close')}</ContextMenuItem>
                        )}
                      </>
                    ) : (
                      <>
                        <ContextMenuItem onSelect={() => startRename(item)}>{t('ai.tabs.rename')}</ContextMenuItem>
                        <ContextMenuItem className="text-destructive" onSelect={() => onAction('closeTab', item)}>{t('terminal.rail.closeTab')}</ContextMenuItem>
                      </>
                    )}
                  </ContextMenuContent>
                </ContextMenu>
              )
            })}

            {g.temporary && reattachable.length > 0 && !collapsed && (
              <>
                <div className="px-3 pt-2 pb-1 text-[11px] text-console-muted">{t('terminal.rail.reattach')}</div>
                {reattachable.map((r) => (
                  <button key={r.session_id} type="button" onClick={() => onReattach(r.session_id)}
                    className={cn(ROW, 'h-9 px-3 hover:bg-console-foreground/5 text-[12px]')}>
                    <History className="h-3.5 w-3.5 shrink-0 text-console-muted" aria-hidden="true" />
                    <span className="font-mono truncate">{r.session_id.slice(0, 12)}</span>
                    <span className="ml-auto text-[10px] text-console-muted shrink-0">{new Date(r.last_use).toLocaleTimeString()}</span>
                  </button>
                ))}
              </>
            )}
          </section>
        ))}
      </div>

      <div className={cn('shrink-0 flex items-center gap-1 border-t border-console-border py-2', collapsed ? 'flex-col px-2' : 'px-2')}>
        <button type="button" onClick={onOpenTools} title={t('terminal.rail.tools')} aria-label={t('terminal.rail.tools')}
          className={cn('flex items-center gap-2 h-8 rounded-lg text-[12px] text-console-muted hover:text-console-foreground hover:bg-console-foreground/5 outline-none focus-visible:ring-2 focus-visible:ring-ring/40', collapsed ? 'w-8 justify-center' : 'flex-1 px-2')}>
          <Wrench className="h-3.5 w-3.5" aria-hidden="true" />
          {!collapsed && t('terminal.rail.tools')}
        </button>
        {onToggleCollapsed && (
          <button type="button" onClick={onToggleCollapsed} title={collapsed ? t('terminal.rail.expand') : t('terminal.rail.collapse')} aria-label={collapsed ? t('terminal.rail.expand') : t('terminal.rail.collapse')}
            className="flex items-center justify-center h-8 w-8 rounded-lg text-console-muted hover:text-console-foreground hover:bg-console-foreground/5 outline-none focus-visible:ring-2 focus-visible:ring-ring/40">
            {collapsed ? <ChevronsRight className="h-3.5 w-3.5" aria-hidden="true" /> : <ChevronsLeft className="h-3.5 w-3.5" aria-hidden="true" />}
          </button>
        )}
      </div>
    </div>
  )
}
