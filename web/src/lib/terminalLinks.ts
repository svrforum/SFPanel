// openTerminalLink opens a link in terminal output, for xterm's web-links
// addon. The addon's own handler opens an empty window and gives it the
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
