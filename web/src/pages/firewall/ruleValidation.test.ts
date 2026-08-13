import { describe, expect, it } from 'vitest'
import { validateFrom, validatePort } from './ruleValidation'

// These two functions gate what reaches the UFW rule builder, so the cases
// below lean on rejecting shell metacharacters and accepting the address
// shapes ufw itself supports (notably IPv6, which an earlier revision of the
// form rejected outright).

describe('validatePort', () => {
  const cases: [name: string, input: string, want: boolean][] = [
    ['single port', '22', true],
    ['port range', '8000:8080', true],
    ['service name', 'ssh', true],
    ['service name with underscore and hyphen', 'my_svc-1', true],
    ['surrounding whitespace is trimmed away', '  22  ', true],
    ['empty string', '', false],
    ['protocol suffix', '80/tcp', false],
    ['comma-separated list', '22,80', false],
    ['shell metacharacters', '22; rm -rf /', false],
    ['command substitution', '$(id)', false],
    ['space-separated pair', '22 80', false],
    ['newline injection', '22\nDROP', false],
    // The regex is a shape check only — it has no numeric range check, so an
    // out-of-range port is accepted here and rejected by ufw server-side.
    ['out-of-range port number', '99999', true],
    // A lone hyphen sits inside the allowed character class.
    ['lone hyphen', '-', true],
  ]

  it.each(cases)('%s', (_name, input, want) => {
    expect(validatePort(input)).toBe(want)
  })
})

describe('validateFrom', () => {
  const cases: [name: string, input: string, want: boolean][] = [
    ['empty means any', '', true],
    ['literal "any"', 'any', true],
    ['IPv4 address', '192.168.1.10', true],
    ['IPv4 CIDR', '10.0.0.0/8', true],
    ['IPv4 host CIDR', '10.0.0.1/32', true],
    ['IPv6 address', '2001:db8::1', true],
    ['IPv6 CIDR', '2001:db8::/32', true],
    ['IPv6 unspecified', '::', true],
    ['IPv6 full form', 'fe80:0000:0000:0000:0202:b3ff:fe1e:8329', true],
    ['surrounding whitespace is trimmed away', '  10.0.0.1  ', true],
    ['hostname', 'example.com', false],
    ['shell metacharacters', '10.0.0.1; id', false],
    ['space-separated pair', '10.0.0.1 10.0.0.2', false],
    ['whitespace only', '   ', false],
    // Case-sensitive: only the exact lowercase sentinel short-circuits, and
    // "ANY" matches neither address regex.
    ['uppercase ANY', 'ANY', false],
    // Both regexes are documented as "basic" — they check shape, not numeric
    // range, so an impossible IPv4 and an over-wide prefix are accepted. ufw
    // rejects them server-side.
    ['out-of-range IPv4 octets and prefix', '999.999.999.999/99', true],
    // Nine hex groups exceeds the {1,7} repeat, so an over-long IPv6 is
    // rejected even though shorter malformed ones are not.
    ['too many IPv6 groups', '1:2:3:4:5:6:7:8:9', false],
  ]

  it.each(cases)('%s', (_name, input, want) => {
    expect(validateFrom(input)).toBe(want)
  })
})
