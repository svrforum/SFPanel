import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronRight } from 'lucide-react'
import { api } from '@/lib/api'
import { hostListeners, type HostListener } from '@/lib/running'

type State =
  | { phase: 'idle' }
  | { phase: 'loading' }
  | { phase: 'ready'; list: HostListener[] }
  | { phase: 'error'; error: string }

/**
 * What listens on the host itself, behind a native disclosure. The port map
 * walks every socket on the node, so it is asked for only when this is
 * opened, and again on each opening.
 */
export function HostListeners({ node }: { node?: string }) {
  const { t } = useTranslation()
  const [state, setState] = useState<State>({ phase: 'idle' })

  const load = useCallback(() => {
    setState({ phase: 'loading' })
    api.getPortMap(node)
      .then((rows) => setState({ phase: 'ready', list: hostListeners(rows ?? []) }))
      .catch((err: unknown) => setState({ phase: 'error', error: err instanceof Error ? err.message : String(err) }))
  }, [node])

  return (
    <details className="group px-4 pb-3" onToggle={(e) => { if (e.currentTarget.open) load() }}>
      <summary className="flex min-h-11 cursor-pointer list-none items-center gap-1.5 rounded-md text-xs font-semibold text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
        <ChevronRight className="size-3.5 shrink-0 transition-transform group-open:rotate-90 motion-reduce:transition-none" aria-hidden="true" />
        {state.phase === 'ready' ? t('running.host.titleCount', { count: state.list.length }) : t('running.host.title')}
      </summary>
      {state.phase === 'loading' && <p role="status" className="py-1 text-xs text-muted-foreground">{t('common.loading')}</p>}
      {state.phase === 'error' && (
        <p role="alert" className="flex flex-wrap items-center gap-x-2 py-1 text-xs text-destructive">
          <span className="break-words">{t('running.host.error')} {state.error}</span>
          <button type="button" onClick={load} className="min-h-11 rounded-md px-2 font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            {t('common.retry')}
          </button>
        </p>
      )}
      {state.phase === 'ready' && (state.list.length === 0
        ? <p className="py-1 text-xs text-muted-foreground">{t('running.host.empty')}</p>
        : (
          <ul className="flex flex-wrap gap-1.5 py-1">
            {state.list.map((l) => (
              <li key={`${l.port}/${l.proto}`} className="break-all rounded-lg bg-secondary px-2 py-1 text-xs">
                {l.process ?? t('running.host.unknownProcess')} :{l.port}{l.proto !== 'tcp' && `/${l.proto}`}
              </li>
            ))}
          </ul>
        ))}
    </details>
  )
}
