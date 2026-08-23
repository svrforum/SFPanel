/**
 * What to put in a config request's gateway field.
 *
 * Three states have to survive the trip, and a plain string only carries two:
 *
 * - `undefined` — leave the saved gateway alone. Sent when the dialog never
 *   learned what it was (the panel cannot read every netplan renderer) and the
 *   operator typed nothing. A box the form could not fill is not a request to
 *   delete the host's default route, which is what sending "" would mean.
 * - `''` — clear it. Sent when the operator is looking at a field they can see
 *   is empty, or when switching to DHCP, where static config goes anyway.
 * - a value — set it.
 *
 * The old code sent `''` in every case, so every save on a static interface
 * deleted the default route. On a remote host that is the last request the
 * panel serves.
 */
export function gatewayField(
  mode: 'dhcp' | 'static',
  value: string,
  known: boolean,
): string | undefined {
  if (mode !== 'static') return ''
  const trimmed = value.trim()
  if (trimmed === '' && !known) return undefined
  return trimmed
}
