import type { AISession } from '@/types/api'

/**
 * A PTY tab — this browser's own, one per server PTY session id. It is what
 * today's Terminal.tsx calls a Tab; the type lives here so the rail can hold
 * both engines' items without importing a component.
 */
export interface PtyTab { id: string; title: string }

export type RailItem =
  | { kind: 'tmux'; id: string; session: AISession }
  | { kind: 'pty'; id: string; tab: PtyTab }

export interface RailGroup {
  /** the cwd for a tmux group; PTY_GROUP_KEY for the temporary group */
  key: string
  /** basename of the cwd; the temporary group's label is the caller's word */
  label: string
  path?: string
  temporary?: boolean
  items: RailItem[]
}

export const PTY_GROUP_KEY = 'pty'

/** What sfpanel_terminal_active:<node> stores: 'tmux:<id>' or 'pty:<id>'. */
export function activeKey(item: RailItem): string {
  return `${item.kind}:${item.id}`
}

/**
 * What a page load restores from the stored active key. Only a `tmux:` value
 * survives a reload; everything else found there names something that cannot
 * exist any more, and is read as "no preference" so pickActive decides:
 *
 * - a bare id is what the PTY-only page wrote, and that page auto-created its
 *   first tab for every visitor rather than on request — honouring it made an
 *   upgraded browser open a phantom temporary shell while live tmux sessions
 *   sat unselected;
 * - a `pty:` value names a temporary tab. Tabs are written here while the page
 *   is open, but nothing persists them, so by the time one is read back the
 *   tab it names is gone.
 *
 * Both are dropped here rather than carried into the page's state, which is
 * what lets Terminal.tsx delete them from storage on load: a value still held
 * in state would be written straight back.
 */
export function parseActiveKey(raw: string | null): string | null {
  if (!raw) return null
  return raw.startsWith('tmux:') ? raw : null
}

export function baseName(cwd: string): string {
  return cwd.replace(/\/+$/, '').split('/').pop() || '/'
}

// A session the panel has no row for reports no created_at; it sorts after
// every dated group rather than throwing the whole order off.
function createdAt(s: AISession): number {
  const n = Date.parse(s.created_at)
  return Number.isNaN(n) ? Number.POSITIVE_INFINITY : n
}

/**
 * Groups tmux sessions by their exact cwd, orders the groups by the oldest
 * session inside each (so a new directory is appended at the bottom instead
 * of reshuffling the list), keeps the server's order inside a group with
 * ended sessions moved last, and appends the temporary (PTY) group when it
 * has tabs or the page is in fallback mode.
 */
export function buildRail(sessions: AISession[], ptyTabs: PtyTab[], opts: { fallback: boolean; temporaryLabel: string }): RailGroup[] {
  const byDir = new Map<string, AISession[]>()
  for (const s of sessions) {
    const list = byDir.get(s.cwd)
    if (list) list.push(s)
    else byDir.set(s.cwd, [s])
  }
  const groups: RailGroup[] = [...byDir.entries()]
    .map(([cwd, list]) => ({ cwd, list, oldest: Math.min(...list.map(createdAt)) }))
    // Infinity - Infinity is NaN, which sort would read as "equal" anyway;
    // the explicit branch keeps the intent visible.
    .sort((a, b) => (a.oldest === b.oldest ? 0 : a.oldest - b.oldest))
    .map(({ cwd, list }) => ({
      key: cwd,
      label: baseName(cwd),
      path: cwd,
      items: [...list.filter((s) => s.state !== 'ended'), ...list.filter((s) => s.state === 'ended')]
        .map((s): RailItem => ({ kind: 'tmux', id: s.id, session: s })),
    }))
  if (ptyTabs.length > 0 || opts.fallback) {
    groups.push({
      key: PTY_GROUP_KEY,
      label: opts.temporaryLabel,
      temporary: true,
      items: ptyTabs.map((tab): RailItem => ({ kind: 'pty', id: tab.id, tab })),
    })
  }
  return groups
}

/**
 * The item to show: the persisted key while it exists, else the live tmux
 * session attached most recently, else the first item, else nothing.
 */
export function pickActive(groups: RailGroup[], persisted: string | null): string | null {
  const items = groups.flatMap((g) => g.items)
  if (items.length === 0) return null
  if (persisted && items.some((i) => activeKey(i) === persisted)) return persisted
  // The winning timestamp is carried alongside the item: last_attached_at is
  // optional on AISession, and the compiler cannot see that the loop only ever
  // stores an item that has one.
  let best: RailItem | null = null
  let bestAt = ''
  for (const i of items) {
    if (i.kind !== 'tmux' || i.session.state === 'ended' || !i.session.last_attached_at) continue
    if (i.session.last_attached_at > bestAt) {
      best = i
      bestAt = i.session.last_attached_at
    }
  }
  return activeKey(best ?? items[0])
}

export function findItem(groups: RailGroup[], key: string | null): RailItem | null {
  if (!key) return null
  for (const g of groups) for (const i of g.items) if (activeKey(i) === key) return i
  return null
}

export type RailNote = 'waiting' | 'toolExited' | 'ended' | null

/**
 * The row's second line, when it says something. A shell session at its
 * prompt is the normal case and gets no line; an AI tool that has dropped
 * back to the shell is worth a word; a waiting session is flagged only when
 * the operator is not already looking at it.
 */
export function railNote(s: AISession, active: boolean): RailNote {
  if (s.state === 'ended') return 'ended'
  if (s.state === 'shell') return s.tool === 'shell' ? null : 'toolExited'
  if (s.state === 'waiting' && !active) return 'waiting'
  return null
}
