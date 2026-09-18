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
