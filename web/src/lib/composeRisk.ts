/**
 * The compose safety analyser refuses a stack in two tiers (see
 * internal/composex/safety.go):
 *
 * - COMPOSE_RISKY — docker.sock, privileged, host namespaces, devices, … The
 *   App Store's own catalog is full of stacks that need them, so this is a
 *   mistake guard, not a security boundary: the operator is shown what the
 *   stack asks for and the same request with `acknowledge_risks: true` goes
 *   through.
 * - COMPOSE_FORBIDDEN — a bind of the panel's own secrets (/etc/sfpanel,
 *   /var/lib/sfpanel, /root/.ssh, /etc/sudoers.d). No acknowledgement lifts it
 *   on the server, so raising a dialog here would only ask a question whose
 *   answer changes nothing. It stays an ordinary error.
 *
 * Both arrive through api.request, which attaches `code` and `status` to the
 * thrown Error; the message is every finding joined with "; ".
 */

interface CodedError {
  code?: string
  message?: string
}

function coded(err: unknown): CodedError | null {
  if (!(err instanceof Error)) return null
  return err as Error & CodedError
}

/** True when this refusal is one the operator can lift by saying yes. */
export function isRiskyRefusal(err: unknown): boolean {
  return coded(err)?.code === 'COMPOSE_RISKY'
}

/** The findings the server listed, one per line, for the confirm dialog. */
export function riskLines(err: unknown): string[] {
  const e = coded(err)
  if (e?.code !== 'COMPOSE_RISKY') return []
  return (e.message ?? '')
    .split('; ')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

/**
 * The findings of a migration pre-flight's `compose-risky` warning, one per
 * line, or [] when the report has none. The server words it "<lead>: a, b" —
 * joined with ", ", not the "; " of a refusal — and a finding quotes its
 * service and path, so a comma inside quotes is not a break.
 */
export function migrationRiskLines(warnings: { code: string; message: string }[] | undefined): string[] {
  const risky = warnings?.find((w) => w.code === 'compose-risky')
  if (!risky) return []
  const colon = risky.message.indexOf(': ')
  const list = colon < 0 ? risky.message : risky.message.slice(colon + 2)
  const lines: string[] = []
  let current = ''
  let quoted = false
  for (let i = 0; i < list.length; i++) {
    const ch = list[i]
    if (ch === '\\' && quoted) {
      current += ch + (list[i + 1] ?? '')
      i++
      continue
    }
    if (ch === '"') quoted = !quoted
    if (!quoted && ch === ',' && list[i + 1] === ' ') {
      lines.push(current)
      current = ''
      i++
      continue
    }
    current += ch
  }
  lines.push(current)
  return lines.map((line) => line.trim()).filter((line) => line.length > 0)
}
