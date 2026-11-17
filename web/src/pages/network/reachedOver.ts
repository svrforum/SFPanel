// reachedOver says whether the browser reached this panel on one of a VPN's
// own addresses — a Tailscale IP or MagicDNS name, a WireGuard interface's
// address — so that taking the VPN down would cut this very page off.
// addresses are as the server lists them: one each, or a comma-separated
// list with prefixes ("10.8.0.1/24, fd00:8::1/64").
// An interface whose config has no Address line is listed with none.
export function reachedOver(host: string | null, addresses: (string | undefined)[], dnsSuffix = ''): boolean {
  if (!host) return false
  const h = host.replace(/^\[|\]$/g, '').toLowerCase()
  const own = addresses
    .flatMap((a) => (a ?? '').split(','))
    .map((a) => a.trim().split('/')[0].toLowerCase())
    .filter(Boolean)
  if (own.includes(h)) return true
  const suffix = dnsSuffix.replace(/\.$/, '').toLowerCase()
  return suffix !== '' && h.endsWith(`.${suffix}`)
}

// panelHost is the host the browser reached this panel on, or null while the
// page shows another cluster node, whose own path to the browser is unknown.
export function panelHost(serverUrl: string | null, currentNode: string | null): string | null {
  if (currentNode) return null
  try {
    return serverUrl ? new URL(serverUrl).hostname : window.location.hostname
  } catch {
    return null
  }
}
