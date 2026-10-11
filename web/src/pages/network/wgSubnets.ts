// vpnSubnets turns a WireGuard interface's Address ("10.8.0.1/24, fd00:8::1/64")
// into the networks it serves ("10.8.0.0/24, fd00:8::/64"): the AllowedIPs a
// client needs to reach the VPN and nothing else. The peer dialog used to give
// every client 0.0.0.0/0, ::/0, a full tunnel through a server the panel never
// sets up to forward or NAT, so a phone that connected lost the internet.
// Entries that do not parse are left out.
export function vpnSubnets(address: string): string {
  const nets: string[] = []
  for (const cidr of address.split(',').map((a) => a.trim()).filter(Boolean)) {
    const [addr, bits] = cidr.split('/')
    const v6 = addr.includes(':')
    const max = v6 ? 128 : 32
    const prefix = bits === undefined ? max : Number(bits)
    if (!Number.isInteger(prefix) || prefix < 0 || prefix > max) continue
    const net = v6 ? ipv6Network(addr, prefix) : ipv4Network(addr, prefix)
    if (net) nets.push(`${net}/${prefix}`)
  }
  return nets.join(', ')
}

function ipv4Network(addr: string, prefix: number): string | null {
  const parts = addr.split('.')
  if (parts.length !== 4 || parts.some((p) => !/^\d{1,3}$/.test(p) || Number(p) > 255)) return null
  return parts
    .map((p, i) => {
      const keep = Math.max(0, Math.min(8, prefix - i * 8))
      return Number(p) & ((0xff << (8 - keep)) & 0xff)
    })
    .join('.')
}

function ipv6Network(addr: string, prefix: number): string | null {
  const halves = addr.split('::')
  if (halves.length > 2) return null
  const head = halves[0] ? halves[0].split(':') : []
  const tail = halves.length === 2 && halves[1] ? halves[1].split(':') : []
  const fill = 8 - head.length - tail.length
  if (halves.length === 1 ? fill !== 0 : fill < 1) return null
  const words = [...head, ...Array<string>(halves.length === 2 ? fill : 0).fill('0'), ...tail]
  if (words.some((w) => !/^[0-9a-fA-F]{1,4}$/.test(w))) return null
  const groups = words.map((w, i) => {
    const keep = Math.max(0, Math.min(16, prefix - i * 16))
    return parseInt(w, 16) & ((0xffff << (16 - keep)) & 0xffff)
  })
  // Shorten the longest run of two or more zero groups to "::".
  let start = -1
  let len = 0
  for (let i = 0; i < 8; ) {
    let j = i
    while (j < 8 && groups[j] === 0) j++
    if (j - i >= 2 && j - i > len) {
      start = i
      len = j - i
    }
    i = j === i ? i + 1 : j
  }
  const hex = groups.map((g) => g.toString(16))
  if (start < 0) return hex.join(':')
  return `${hex.slice(0, start).join(':')}::${hex.slice(start + len).join(':')}`
}
