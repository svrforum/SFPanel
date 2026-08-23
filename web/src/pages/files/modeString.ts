/**
 * Turn a mode string like "-rwxr-xr--" or "drwxr-xr-x" into three octal digits.
 *
 * The listing already carries this string and the panel already displays it, so
 * the permissions dialog starts from what is on screen rather than making a
 * second round trip for something it was already told.
 *
 * Getting this wrong is not cosmetic: the dialog opens pre-filled, and a
 * misread turns "open the permissions of this file" into "silently change
 * them".
 */
export function modeStringToOctal(mode: string): string {
  const perms = mode.length >= 10 ? mode.slice(1, 10) : ''
  // Validate the SHAPE, not just the length. A length check alone lets any
  // seventeen-character string through — mode.slice(1, 10) of it is nine
  // characters, none of them permission characters, and the result is "000".
  // Applying that locks the file, which is a considerably worse outcome than
  // falling back to a sane default.
  if (!/^[r-][w-][xsStT-][r-][w-][xsStT-][r-][w-][xsStT-]$/.test(perms)) return '644'
  let out = ''
  for (let i = 0; i < 9; i += 3) {
    let digit = 0
    if (perms[i] === 'r') digit += 4
    if (perms[i + 1] === 'w') digit += 2
    // s/t in the execute slot means setuid, setgid or sticky is set. Lowercase
    // means the execute bit is on as well; uppercase means it is not. Either
    // way this dialog edits only the ordinary bits — the server refuses to set
    // the special ones — so the special bit itself is neither read nor written.
    if (perms[i + 2] === 'x' || perms[i + 2] === 's' || perms[i + 2] === 't') digit += 1
    out += String(digit)
  }
  return out
}
