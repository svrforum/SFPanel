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
 * Reads a stored active key. Only an engine-prefixed value means anything:
 * the PTY-only page kept a bare tab id under the same localStorage name, and
 * it auto-created that first tab for every visitor rather than on request. A
 * bare value is therefore not a session the operator chose — honouring it made
 * an upgraded browser open a phantom temporary shell while live tmux sessions
 * sat unselected — so it is read as "no preference" and pickActive decides.
 */
export function parseActiveKey(raw: string | null): string | null {
  if (!raw) return null
  return raw.startsWith('tmux:') || raw.startsWith('pty:') ? raw : null
}

/**
 * The PTY tabs worth keeping. A tab is a pointer to a server-side PTY
 * session, and the server creates a new session for an id it does not know
 * (internal/feature/terminal/handler.go), so a tab whose session has been
 * reaped — five minutes with no reader — would silently open a fresh shell
 * instead of reporting that it is gone.
 *
 * `eligible` is the set of ids that were loaded from storage at mount. A tab
 * created during this page's life is never pruned: its socket may not have
 * registered a session by the time the list arrives.
 *
 * Returns the input array unchanged when nothing is dropped, so the caller
 * can skip a state update and the render it would cause.
 */
export function prunePtyTabs(stored: PtyTab[], serverIds: string[], eligible: string[]): PtyTab[] {
  const alive = new Set(serverIds)
  const prunable = new Set(eligible)
  const kept = stored.filter((tab) => !prunable.has(tab.id) || alive.has(tab.id))
  return kept.length === stored.length ? stored : kept
}

/**
 * The PTY tabs the pane may mount. Connecting to a PTY id the server does not
 * know makes it CREATE that session, so mounting a tab restored from storage
 * before GET /terminal/sessions has answered manufactures exactly the shell
 * prunePtyTabs exists to avoid: the pane's sessions connect before the page's
 * own effect runs, and the prune that follows drops the tab only after its
 * shell has been spawned.
 *
 * `restored` is the ids loaded from storage at mount; `checked` says the list
 * has answered — a FAILED request counts, because an unreachable server must
 * not keep a tab that may well be alive off screen for good. A tab this page
 * created is never held back. Returns the input array unchanged when nothing
 * is held back, so the pane is handed the same identity as `tabs`.
 */
export function mountablePtyTabs(tabs: PtyTab[], restored: string[], checked: boolean): PtyTab[] {
  if (checked) return tabs
  const held = new Set(restored)
  const open = tabs.filter((tab) => !held.has(tab.id))
  return open.length === tabs.length ? tabs : open
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
