// OSC 52 is how a terminal program asks the terminal to set the clipboard:
// ESC ] 52 ; <selection> ; <base64 text> BEL. tmux sends one when a mouse drag
// — or a double or triple click — copies in a panel session, and drops its
// highlight on release; with nothing receiving it, the copy went nowhere.
//
// This decodes the payload xterm hands an OSC handler (everything after
// "52;"). Only a write comes back as text: "?" asks the terminal to send the
// clipboard back to the program, which would hand the operator's clipboard to
// whatever runs on the server, so it is never answered. The selection field
// (c, p, s, 0-7 or empty) is ignored — the browser has one clipboard.
export function decodeOsc52(data: string): string | null {
  const sep = data.indexOf(';')
  if (sep < 0) return null
  const payload = data.slice(sep + 1)
  if (payload === '' || payload === '?') return null
  let binary: string
  try {
    binary = atob(payload)
  } catch {
    return null
  }
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(binary, (c) => c.charCodeAt(0)))
  } catch {
    return null
  }
}
