import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { ClipboardPaste, Copy, TextSelect } from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IS_MAC } from '@/lib/terminalKeys'

/** Where the right click landed, and whether there was a selection to copy. */
export interface TerminalMenuAt { x: number; y: number; canCopy: boolean; open: boolean }

// The terminal's right-click menu, opened at the pointer: a zero-size trigger
// sits where the click landed and the menu hangs from it. Not modal, so a
// second right click lands on the terminal and moves the menu, instead of
// falling through a page-wide overlay to the browser's own menu.
//
// Copy and paste run on the click itself — the clipboard wants the gesture —
// and the keyboard goes back to the terminal as the menu closes, unless the
// click that closed it went somewhere else. The text view is a dialog of its
// own, so it opens only once the menu has let go of focus.
export function TerminalContextMenu({ at, onOpenChange, onCopy, onPaste, onSelectText, onClosed }: {
  at: TerminalMenuAt
  onOpenChange: (open: boolean) => void
  onCopy: () => void
  onPaste: () => void
  onSelectText: () => void
  /** The menu closed on its own terms (an item, Escape): give the terminal the keyboard. */
  onClosed: () => void
}) {
  const { t } = useTranslation()
  const outside = useRef(false)
  const viewAll = useRef(false)
  return (
    <DropdownMenu open={at.open} onOpenChange={onOpenChange} modal={false}>
      <DropdownMenuTrigger asChild>
        <span aria-hidden="true" tabIndex={-1} className="fixed size-0" style={{ left: at.x, top: at.y }} />
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        aria-label={t('terminal.menu.label')}
        onInteractOutside={() => { outside.current = true }}
        onCloseAutoFocus={(e) => {
          e.preventDefault()
          if (!outside.current) onClosed()
          outside.current = false
          if (viewAll.current) {
            viewAll.current = false
            onSelectText()
          }
        }}
      >
        <DropdownMenuItem disabled={!at.canCopy} onSelect={onCopy}>
          <Copy aria-hidden="true" />{t('terminal.menu.copy')}
          <DropdownMenuShortcut>{IS_MAC ? '⌘C' : 'Ctrl+Shift+C'}</DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onPaste}>
          <ClipboardPaste aria-hidden="true" />{t('terminal.menu.paste')}
          <DropdownMenuShortcut>{IS_MAC ? '⌘V' : 'Ctrl+V'}</DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => { viewAll.current = true }}>
          <TextSelect aria-hidden="true" />{t('terminal.menu.selectText')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
