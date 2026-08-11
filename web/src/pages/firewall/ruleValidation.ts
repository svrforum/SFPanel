// Shared input validation for the two UFW rule entry points
// (FirewallRules add/edit dialogs and the FirewallPorts add-to-UFW dialog).

// Port validation: number, range (8000:8080), or service name
const PORT_REGEX = /^[a-zA-Z0-9_-]+(:[a-zA-Z0-9_-]+)?$/
// IPv4/CIDR validation (basic)
const IPV4_CIDR_REGEX = /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/
// IPv6/CIDR validation (basic: hex groups incl. '::' shorthand) — ufw accepts
// IPv6 sources (e.g. 2001:db8::/32), so the form must not reject them.
const IPV6_CIDR_REGEX = /^[0-9a-fA-F]{0,4}(:[0-9a-fA-F]{0,4}){1,7}(\/\d{1,3})?$/

export function validatePort(port: string): boolean {
  return PORT_REGEX.test(port.trim())
}

export function validateFrom(from: string): boolean {
  if (!from || from === 'any') return true
  const v = from.trim()
  return IPV4_CIDR_REGEX.test(v) || IPV6_CIDR_REGEX.test(v)
}
