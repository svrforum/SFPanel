import type { ILinkProvider, Terminal } from '@xterm/xterm'
import { urlsAt } from './terminalWord'

// openTerminalLink opens a link in terminal output. xterm's web-links addon,
// which this file once fed, opened an empty window by default and gave it the
// address afterwards. In the Android app's WebView, which has no second
// window, that empty window.open() hands back the page itself and spends the
// tap's user activation, so the address that follows reads as the page
// navigating on its own — and the app, which offers to open an outside link
// only for a navigation the user started, drops it without a word. With the
// address in the window.open call itself, a browser opens a tab and the app
// asks to open the link in the phone's browser.
export function openTerminalLink(_event: MouseEvent, uri: string): void {
  window.open(uri, '_blank', 'noopener,noreferrer')
}

// terminalLinkProvider finds the links in terminal output: every URL a row is
// part of, whole even where the program broke it into rows of its own (see
// urlRunsOn). It stands in for xterm's web-links addon, which follows only the
// rows the screen itself wrapped, and so opened Claude Code's login URL cut
// off at the edge of a phone's screen.
export function terminalLinkProvider(term: Terminal): ILinkProvider {
  return {
    provideLinks(y, callback) {
      const links = urlsAt(term.buffer.active, term.cols, y - 1).map((url) => ({
        text: url.text,
        // xterm's ranges count from 1 and include both ends.
        range: { start: { x: url.start.col + 1, y: url.start.row + 1 }, end: { x: url.end.col + 1, y: url.end.row + 1 } },
        activate: openTerminalLink,
      }))
      callback(links.length > 0 ? links : undefined)
    },
  }
}
