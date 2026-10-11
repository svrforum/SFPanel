// Shared input validation for the two UFW rule entry points
// (FirewallRules add/edit dialogs and the FirewallPorts add-to-UFW dialog).

// Port validation: number, range (8000:8080), or service name — or a list of
// numbers and ranges (80,443,8000:8080), which the field's example has always
// shown and the check used to refuse. ufw takes a list of numbers only.
const PORT_REGEX = /^(?:[a-zA-Z0-9_-]+(?::[a-zA-Z0-9_-]+)?|\d+(?::\d+)?(?:,\d+(?::\d+)?)+)$/
// IPv4/CIDR validation (basic)
const IPV4_CIDR_REGEX = /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/
// IPv6/CIDR validation (basic: hex groups incl. '::' shorthand) — ufw accepts
// IPv6 sources (e.g. 2001:db8::/32), so the form must not reject them.
const IPV6_CIDR_REGEX = /^[0-9a-fA-F]{0,4}(:[0-9a-fA-F]{0,4}){1,7}(\/\d{1,3})?$/

// normalizePort drops the spaces the example puts after its commas; ufw takes
// none.
export function normalizePort(port: string): string {
  return port.trim().replace(/\s*,\s*/g, ',')
}

export function validatePort(port: string): boolean {
  return PORT_REGEX.test(normalizePort(port))
}

// ufw refuses a range or a list without tcp or udp.
export function portNeedsProtocol(port: string): boolean {
  return /[,:]/.test(normalizePort(port))
}

export function validateFrom(from: string): boolean {
  if (!from || from === 'any') return true
  const v = from.trim()
  return IPV4_CIDR_REGEX.test(v) || IPV6_CIDR_REGEX.test(v)
}
