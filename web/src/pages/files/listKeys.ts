/** The part of an element the key handling reads, so it decides without a DOM. */
export interface KeyNode {
  tagName: string
  isContentEditable?: boolean
  parentElement: KeyNode | null
  hasAttribute(name: string): boolean
  getAttribute(name: string): string | null
}

export type ListKey =
  /** to another row; `row` is the one the key was pressed in, or -1 */
  | { action: 'move'; to: 'next' | 'prev' | 'first' | 'last'; row: number }
  | { action: 'toggle'; row: number }
  | { action: 'parent' }

const MOVES: Record<string, 'next' | 'prev' | 'first' | 'last'> = { ArrowDown: 'next', ArrowUp: 'prev', Home: 'first', End: 'last' }
const FIELDS = new Set(['INPUT', 'TEXTAREA', 'SELECT'])

/**
 * What a key pressed on the file manager does to the listing, or null when
 * the key is not the listing's.
 *
 * The handler sits on the page, so the keys of every control on it pass
 * through — and React bubbles the keys of the dialogs and menus the page
 * renders to it as well, though they are portalled out of its element. It used
 * to take Enter, Space and Backspace from all of them: Enter on New folder
 * opened the active row, Space in the permissions dialog ticked a row behind
 * it, Backspace in a dialog left the folder. So:
 *  - a key from outside the page's element (a portal) is not the listing's,
 *    nor one typed into a field, nor one with Ctrl, Alt or ⌘;
 *  - the arrows, Home and End move between rows from anywhere on the page;
 *  - Space selects on a row's name, where the arrows put the focus, and
 *    elsewhere presses what has the focus;
 *  - Backspace goes up a folder from a row or from the page itself;
 *  - Enter is never taken: the focus is on a row's name, which opens it.
 */
export function listKey(e: { key: string; ctrlKey: boolean; altKey: boolean; metaKey: boolean; target: KeyNode; currentTarget: KeyNode }): ListKey | null {
  if (e.ctrlKey || e.altKey || e.metaKey) return null
  const target = e.target
  let rowNode: KeyNode | null = null
  let node: KeyNode | null = target
  while (node && node !== e.currentTarget) {
    if (!rowNode && node.hasAttribute('data-row-index')) rowNode = node
    node = node.parentElement
  }
  if (node !== e.currentTarget) return null
  if (FIELDS.has(target.tagName) || target.isContentEditable) return null
  const row = rowNode ? Number(rowNode.getAttribute('data-row-index')) : -1

  const to = MOVES[e.key]
  if (to) return { action: 'move', to, row }
  if (e.key === ' ') return rowNode && (target === rowNode || target.hasAttribute('data-row-open')) ? { action: 'toggle', row } : null
  if (e.key === 'Backspace') return rowNode || target === e.currentTarget ? { action: 'parent' } : null
  return null
}
