import { useEffect, useRef, useState, type RefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { Eraser, Minus, MoreHorizontal, PanelLeft, Plus, Search, ShieldAlert, User as UserIcon, X } from 'lucide-react'
import type { TerminalInfo } from '@/types/api'
import { TOOL_META, dangerousFlagFor, launchSummary } from '@/lib/aiSessions'
import { activeKey, type RailItem } from '@/lib/sessionRail'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import type { RailAction } from '@/pages/terminal/components/SessionRail'

export interface SessionSearch {
  open: boolean
  query: string
  inputRef: RefObject<HTMLInputElement | null>
  onToggle: () => void
  onQuery: (q: string) => void
  onNext: () => void
  onPrev: () => void
  onClose: () => void
}

export interface SessionHeaderProps {
  item: RailItem | null
  hostInfo: TerminalInfo | null
  fontSize: number
  onFontSize: (delta: number) => void
  search: SessionSearch
  onClear: () => void
  onRename: (item: RailItem, title: string) => void
  onAction: (action: RailAction, item: RailItem) => void
  /** present on a phone: the drawer trigger with the waiting count */
  drawer?: { onOpen: () => void; waiting: number }
}

const ICON = 'h-7 w-7 p-0 text-console-muted hover:text-console-foreground hover:bg-console-foreground/10'

/**
 * The strip above the pane: who this session is, the toolbar the old
 * terminal page kept in its tab bar (font size, search, clear), the
 * user@host badge, and one menu holding the same actions as the rail row —
 * so every action exists somewhere visible, and on a phone (where the rail
 * is in a drawer) the Android app can open them through [data-session-menu].
 */
export function SessionHeader({ item, hostInfo, fontSize, onFontSize, search, onClear, onRename, onAction, drawer }: SessionHeaderProps) {
  const { t } = useTranslation()
  // Destructured once, not read as `search.…` through the JSX: the search
  // object carries the input's ref, and react-hooks/refs treats every
  // property read on an object holding one as a ref access during render.
  // Taking the fields apart here is the same read the rule allows for a
  // local ref — nothing dereferences `.current` in render either way.
  const { open: searchOpen, query: searchQuery, inputRef: searchInputRef,
    onToggle: onSearchToggle, onQuery: onSearchQuery, onNext: onSearchNext,
    onPrev: onSearchPrev, onClose: onSearchClose } = search
  // The rename in progress is keyed to the session it started on, so a
  // switch to another session ends it without an effect having to notice.
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [name, setName] = useState('')
  const editRef = useRef<HTMLInputElement>(null)
  const key = item ? activeKey(item) : null
  const editing = key !== null && editingKey === key
  useEffect(() => { if (editing) editRef.current?.select() }, [editing])

  const s = item?.kind === 'tmux' ? item.session : null
  const meta = s ? (TOOL_META[s.tool] ?? TOOL_META.shell) : TOOL_META.shell
  const title = !item ? '' : item.kind === 'tmux' ? item.session.title : item.tab.title
  const launch = s ? launchSummary(s.tool, s.launch, t) : ''
  const dangerLabel = s ? t('ai.tabs.dangerousLaunch', { flag: dangerousFlagFor(s.tool) }) : ''
  const startEditing = () => { if (item && !s?.unknown) { setName(title); setEditingKey(key) } }
  const commit = () => {
    setEditingKey(null)
    if (item && name.trim()) onRename(item, name.trim())
  }
  const user = item?.kind === 'pty' ? (hostInfo?.shell_user ?? t('terminal.header.temporaryUser')) : s?.run_as
  const isRoot = item?.kind === 'pty' ? hostInfo?.is_root === true : s?.run_as === 'root'

  return (
    <header data-terminal-chrome className="shrink-0 border-b border-console-border text-console-foreground">
      <div className="flex items-center gap-2 h-11 px-2 md:px-3">
        {drawer && (
          <button type="button" onClick={drawer.onOpen} aria-label={t('terminal.rail.sessions')}
            className="md:hidden relative flex items-center justify-center h-8 w-8 rounded-lg text-console-muted hover:text-console-foreground hover:bg-console-foreground/10 outline-none focus-visible:ring-2 focus-visible:ring-ring/40">
            <PanelLeft className="h-4 w-4" aria-hidden="true" />
            {drawer.waiting > 0 && (
              <span className="absolute -top-0.5 -right-0.5 min-w-4 h-4 px-1 rounded-full bg-warning text-warning-foreground text-[10px] font-semibold flex items-center justify-center"
                aria-label={t('terminal.rail.waitingCount', { n: drawer.waiting })}>{drawer.waiting}</span>
            )}
          </button>
        )}
        {item && (
          <span className="h-[18px] w-[18px] rounded-md flex items-center justify-center text-[10px] font-bold shrink-0"
            style={{ backgroundColor: `${meta.color}1a`, color: meta.color }} aria-hidden="true">{meta.initial}</span>
        )}
        <div className="min-w-0 flex-1">
          {item ? (
            editing ? (
              <input ref={editRef} value={name} onChange={(e) => setName(e.target.value)} onBlur={commit}
                onKeyDown={(e) => { if (e.key === 'Enter') commit(); if (e.key === 'Escape') setEditingKey(null); e.stopPropagation() }}
                className="w-full max-w-xs bg-transparent border-b border-primary outline-none text-[14px] font-semibold text-console-foreground" maxLength={64} autoFocus />
            ) : (
              <button type="button" onClick={startEditing} title={t('ai.tabs.rename')}
                className="block max-w-full truncate text-[14px] font-semibold text-left outline-none rounded focus-visible:ring-2 focus-visible:ring-ring/40">{title}</button>
            )
          ) : (
            <span className="text-[14px] font-semibold text-console-muted">{t('terminal.header.noSession')}</span>
          )}
          {item && !editing && (
            <div className="hidden md:flex items-center gap-1.5 text-[11px] text-console-muted min-w-0">
              {s ? (
                <>
                  <span className="font-mono">{s.run_as}</span>
                  <span aria-hidden="true">·</span>
                  <span className="font-mono truncate" title={s.cwd}>{s.cwd}</span>
                  {s.profile && <span className="px-1 rounded bg-console-foreground/10 font-mono" title={`${t('ai.profiles.label')}: ${s.profile}`}>{s.profile}</span>}
                  {launch && <><span aria-hidden="true">·</span><span className="truncate" title={launch}>{launch}</span></>}
                  {s.launch?.dangerous && s.state !== 'ended' && (
                    <span className="shrink-0 flex items-center" role="img" aria-label={dangerLabel} title={dangerLabel}>
                      <ShieldAlert className="h-3 w-3 text-destructive" aria-hidden="true" />
                    </span>
                  )}
                </>
              ) : (
                <span>{t('terminal.header.temporaryLifetime')}</span>
              )}
            </div>
          )}
        </div>

        <div className="hidden md:flex items-center gap-0.5 shrink-0">
          <Button variant="ghost" size="sm" className={ICON} onClick={() => onFontSize(-1)} title={t('terminal.fontSmaller')} aria-label={t('terminal.fontSmaller')}><Minus className="h-3 w-3" /></Button>
          <span className="text-[10px] text-console-muted min-w-[20px] text-center">{fontSize}</span>
          <Button variant="ghost" size="sm" className={ICON} onClick={() => onFontSize(1)} title={t('terminal.fontLarger')} aria-label={t('terminal.fontLarger')}><Plus className="h-3 w-3" /></Button>
          <div className="w-px h-4 bg-console-border mx-1" aria-hidden="true" />
          <Button variant="ghost" size="sm" className={cn(ICON, searchOpen && 'text-primary')} onClick={onSearchToggle} title={t('terminal.search')} aria-label={t('terminal.search')}><Search className="h-3.5 w-3.5" /></Button>
          <Button variant="ghost" size="sm" className={ICON} onClick={onClear} title={t('terminal.clear')} aria-label={t('terminal.clear')}><Eraser className="h-3.5 w-3.5" /></Button>
        </div>

        {item && user && (
          <div className={cn('hidden lg:flex items-center gap-1.5 shrink-0 px-2 py-1 rounded-md border text-[11px] font-mono',
            isRoot ? 'bg-warning/10 border-warning/30 text-warning' : 'bg-console-foreground/5 border-transparent text-console-muted')}
            title={isRoot
              ? t('terminal.shellBadgeRootHint', { host: hostInfo?.hostname ?? '', defaultValue: 'Running as root on {{host}} — commands here are unrestricted' })
              : t('terminal.shellBadgeHint', { user, host: hostInfo?.hostname ?? '', defaultValue: 'Connected as {{user}} on {{host}}' })}>
            {isRoot ? <ShieldAlert className="h-3 w-3 shrink-0" aria-hidden="true" /> : <UserIcon className="h-3 w-3 shrink-0" aria-hidden="true" />}
            <span className="truncate max-w-[22ch]">{user}{hostInfo?.hostname ? `@${hostInfo.hostname}` : ''}</span>
          </div>
        )}

        {item && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="sm" className={ICON} data-session-menu title={t('terminal.header.menu')} aria-label={t('terminal.header.menu')}>
                <MoreHorizontal className="h-4 w-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-52">
              <DropdownMenuItem className="md:hidden" onSelect={onSearchToggle}>{t('terminal.search')}</DropdownMenuItem>
              <DropdownMenuItem className="md:hidden" onSelect={onClear}>{t('terminal.clear')}</DropdownMenuItem>
              <DropdownMenuItem className="md:hidden" onSelect={() => onFontSize(1)}>{t('terminal.fontLarger')}</DropdownMenuItem>
              <DropdownMenuItem className="md:hidden" onSelect={() => onFontSize(-1)}>{t('terminal.fontSmaller')}</DropdownMenuItem>
              <DropdownMenuSeparator className="md:hidden" />
              <DropdownMenuItem onSelect={startEditing} disabled={s?.unknown}>{t('ai.tabs.rename')}</DropdownMenuItem>
              {s ? (
                <>
                  <DropdownMenuItem onSelect={() => onAction('info', item)}>{t('ai.tabs.info')}</DropdownMenuItem>
                  <DropdownMenuSeparator />
                  {s.state === 'shell' && s.tool !== 'shell' && !s.unknown && (
                    <DropdownMenuItem onSelect={() => onAction('rerun', item)}>{t('ai.tabs.rerun')}</DropdownMenuItem>
                  )}
                  {s.state === 'ended' ? (
                    <>
                      <DropdownMenuItem onSelect={() => onAction('restart', item)}>{t('ai.tabs.restart')}</DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => onAction('removeEnded', item)}>{t('ai.tabs.removeEnded')}</DropdownMenuItem>
                    </>
                  ) : (
                    <DropdownMenuItem className="text-destructive" onSelect={() => onAction('kill', item)}>{t('ai.tabs.close')}</DropdownMenuItem>
                  )}
                </>
              ) : (
                <DropdownMenuItem className="text-destructive" onSelect={() => onAction('closeTab', item)}>{t('terminal.rail.closeTab')}</DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>

      {searchOpen && (
        <div className="flex items-center gap-1.5 border-t border-console-border px-2 md:px-3 py-1.5">
          <Search className="h-3.5 w-3.5 text-console-muted" aria-hidden="true" />
          <Input ref={searchInputRef} value={searchQuery} onChange={(e) => onSearchQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); if (e.shiftKey) onSearchPrev(); else onSearchNext() }
              if (e.key === 'Escape') onSearchClose()
            }}
            placeholder={t('terminal.searchPlaceholder')} className="h-7 text-xs bg-card border-border text-foreground flex-1 max-w-[10rem] md:max-w-xs" autoFocus />
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs text-console-muted hover:text-console-foreground hover:bg-console-foreground/10" onClick={onSearchPrev}>
            <span className="hidden md:inline">{t('terminal.prev')}</span><span className="md:hidden">↑</span>
          </Button>
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs text-console-muted hover:text-console-foreground hover:bg-console-foreground/10" onClick={onSearchNext}>
            <span className="hidden md:inline">{t('terminal.next')}</span><span className="md:hidden">↓</span>
          </Button>
          <Button variant="ghost" size="sm" className={ICON} onClick={onSearchClose} aria-label={t('common.close')}><X className="h-3.5 w-3.5" /></Button>
        </div>
      )}
    </header>
  )
}
