import type { ServiceInfo } from '@/types/api'

export type ServiceFilter = 'all' | 'running' | 'failed' | 'inactive'

type Unit = Pick<ServiceInfo, 'name' | 'active_state' | 'sub_state'>

export function matchesFilter(s: Pick<ServiceInfo, 'active_state' | 'sub_state'>, filter: ServiceFilter): boolean {
  switch (filter) {
    case 'running':
      return s.active_state === 'active' && s.sub_state === 'running'
    case 'failed':
      return s.active_state === 'failed'
    case 'inactive':
      return s.active_state === 'inactive'
    default:
      return true
  }
}

// By name, and under "All" the failed units first: a host lists a couple of
// hundred units, and by name alone the ones that failed sat somewhere in the
// middle of a long scroll.
export function sortServices<T extends Unit>(list: T[], filter: ServiceFilter): T[] {
  const byName = (a: T, b: T) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())
  return [...list].sort((a, b) => {
    if (filter === 'all') {
      const af = a.active_state === 'failed' ? 0 : 1
      const bf = b.active_state === 'failed' ? 0 : 1
      if (af !== bf) return af - bf
    }
    return byName(a, b)
  })
}

// systemd's sub-states for a service (systemd.service(5)), grouped into the
// words the list shows. The filters use the same words.
const SUB_STATE: Record<string, string> = {
  running: 'running',
  exited: 'exited',
  dead: 'dead',
  failed: 'failed',
  'auto-restart': 'autoRestart',
  condition: 'starting',
  'start-pre': 'starting',
  start: 'starting',
  'start-post': 'starting',
  stop: 'stopping',
  'stop-watchdog': 'stopping',
  'stop-sigterm': 'stopping',
  'stop-sigkill': 'stopping',
  'stop-post': 'stopping',
  'final-watchdog': 'stopping',
  'final-sigterm': 'stopping',
  'final-sigkill': 'stopping',
  reload: 'reloading',
  'reload-signal': 'reloading',
  'reload-notify': 'reloading',
}

const ACTIVE_STATE: Record<string, string> = {
  active: 'active',
  inactive: 'dead',
  failed: 'failed',
  activating: 'starting',
  deactivating: 'stopping',
  reloading: 'reloading',
}

/** The locale key for a unit's state, or null for a word this list does not know (shown as sent). */
export function stateLabelKey(activeState: string, subState: string): string | null {
  const word = subState ? SUB_STATE[subState] : ACTIVE_STATE[activeState]
  return word ? `services.state.${word}` : null
}

// list-unit-files' states; "unknown" is the server's word for a unit it found
// in list-units but not there.
const BOOT_STATE: Record<string, string> = {
  enabled: 'enabled',
  'enabled-runtime': 'enabledRuntime',
  disabled: 'disabled',
  static: 'static',
  masked: 'masked',
  'masked-runtime': 'masked',
  indirect: 'indirect',
  generated: 'generated',
  transient: 'transient',
  alias: 'alias',
  linked: 'linked',
  'linked-runtime': 'linked',
  unknown: 'unknown',
}

/** The locale key for a unit's boot state, or null for a word this list does not know. */
export function bootLabelKey(enabled: string): string | null {
  const word = BOOT_STATE[enabled]
  return word ? `services.bootState.${word}` : null
}
