import type { LucideIcon } from 'lucide-react'

/**
 * One per-row action, rendered in three places from a single definition: the
 * desktop row's icon buttons, its right-click menu, and the mobile card's
 * overflow menu.
 *
 * Keeping one list is the point. Before the context menu existed there were
 * two hand-synced copies of the same five actions; adding a third surface for
 * phones without consolidating would have made three.
 */
export interface EntryAction {
  key: string
  Icon: LucideIcon
  show: boolean
  /** Row button only — the menus cover it via their own open/edit item. */
  rowOnly?: boolean
  iconClassName?: string
  destructive?: boolean
  /** Terse, for an icon button's tooltip. */
  label: string
  /** Fuller, for a menu row that has space for words. */
  menuLabel: string
  onClick: () => void
}
