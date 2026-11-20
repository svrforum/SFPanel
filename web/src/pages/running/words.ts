import type { TFunction } from 'i18next'
import type { StatusPillTone } from '@/components/StatusPill'
import { containerProblem, guestResources, stackInPart, type Age, type RunningContainer, type RunningGuest, type RunningStack } from '@/lib/running'

/** "2시간 전" / "2 hours ago" for an age Docker gave in its own words. */
export function ageText(age: Age, lang: string): string {
  // -0 rather than 0: Intl reads the sign, and +0 says "in 0 seconds".
  return new Intl.RelativeTimeFormat(lang, { numeric: 'always' }).format(age.value === 0 ? -0 : -age.value, age.unit)
}

/** How a container that is not running ended: when, and with what code if not 0. */
export function exitText(c: RunningContainer, t: TFunction, lang: string): string {
  const code = c.exitCode
  if (c.exitedAgo) {
    const ago = ageText(c.exitedAgo, lang)
    return code ? t('running.exitedCode', { ago, code }) : t('running.exited', { ago })
  }
  return code ? t('running.exitedCodeOnly', { code }) : t('running.state.exited')
}

/** A container's state in one word, and the tone it is drawn in. */
export function containerWord(c: RunningContainer, t: TFunction): { text: string; tone: StatusPillTone } {
  const problem = containerProblem(c) !== null
  if (c.state.toLowerCase() === 'dead') return { text: t('running.state.dead'), tone: 'destructive' }
  switch (c.health) {
    case 'running':
      return { text: t('running.state.running'), tone: 'success' }
    case 'unhealthy':
      return { text: t('running.state.unhealthy'), tone: 'warning' }
    case 'restarting':
      return { text: t('running.state.restarting'), tone: 'warning' }
    case 'crashed':
      return { text: t('running.state.exited'), tone: problem ? 'destructive' : 'muted' }
    default: {
      const words: Record<string, string> = {
        exited: t('running.state.exited'),
        created: t('running.state.created'),
        paused: t('running.state.paused'),
        removing: t('running.state.removing'),
      }
      const state = c.state.toLowerCase()
      return { text: Object.hasOwn(words, state) ? words[state] : c.state, tone: 'muted' }
    }
  }
}

/** The stack's word with its count: "일부 실행 3/4". A stack with no containers has no count. */
export function stackWord(s: RunningStack, attention: boolean, t: TFunction): { text: string; tone: StatusPillTone } {
  const word = { running: t('running.stack.running'), partial: t('running.stack.partial'), stopped: t('running.stack.stopped') }[s.state]
  const text = s.total > 0 ? `${word} ${s.running}/${s.total}` : word
  if (s.state === 'running') return { text, tone: 'success' }
  // In part only because a job ended with code 0: said, not warned of.
  if (s.state === 'partial') return { text, tone: stackInPart(s) ? 'warning' : 'muted' }
  return { text, tone: attention ? 'destructive' : 'muted' }
}

/**
 * What a stack's service line says of a container that is not plainly
 * running: how it ended, or its state word. Nothing for one that runs.
 */
export function serviceNote(c: RunningContainer, t: TFunction, lang: string): string | null {
  if (c.health === 'running') return null
  if (c.state.toLowerCase() === 'exited') return exitText(c, t, lang)
  return containerWord(c, t).text
}

/**
 * A guest's state in words. Running is the only state drawn in a colour of
 * its own: a guest that is not running was stopped by someone, and is no
 * finding of the board's.
 */
export function guestWord(g: RunningGuest, t: TFunction): { text: string; tone: StatusPillTone } {
  if (!g.state) return { text: g.status || t('running.guest.state.unknown'), tone: 'muted' }
  const words = {
    running: t('running.guest.state.running'),
    stopped: t('running.guest.state.stopped'),
    paused: t('running.guest.state.paused'),
    suspended: t('running.guest.state.suspended'),
    unknown: t('running.guest.state.unknown'),
  }
  return { text: words[g.state], tone: g.state === 'running' ? 'success' : 'muted' }
}

/** "VM 101 · CPU 4 · 메모리 1.0 GB / 4.0 GB"; the kind is left out where it already is the title. */
export function guestMeta(g: RunningGuest, t: TFunction): string {
  const { cpus, memory } = guestResources(g)
  const kind = g.kind === 'lxc' ? t('running.guest.lxc', { vmid: g.vmid }) : t('running.guest.vm', { vmid: g.vmid })
  return [
    g.name ? kind : null,
    cpus ? t('running.guest.cpus', { cpus }) : null,
    memory ? t('running.guest.memory', { memory }) : null,
  ].filter(Boolean).join(' · ')
}
