import { useTranslation } from 'react-i18next'
import type {
  DiffImageChange,
  DiffSetChange,
  DiffScalarChange,
  DiffHealthcheckChange,
} from '@/types/api'

type RowProps =
  | { kind: 'image'; change: DiffImageChange }
  | { kind: 'set';   change: DiffSetChange }
  | { kind: 'scalar'; change: DiffScalarChange }
  | { kind: 'healthcheck'; change: DiffHealthcheckChange }

export function DiffServiceRow(props: RowProps) {
  const { t } = useTranslation()
  const none = t('compose.diff.none', '(none)')
  return (
    <div className="grid grid-cols-[120px_1fr] gap-2 py-1 text-[12px]">
      <span className="font-medium truncate" title={props.change.service}>{props.change.service}</span>
      <div className="font-mono leading-relaxed">
        {props.kind === 'image' && (
          <span>
            <span>{props.change.from || none}</span>
            <span className="text-muted-foreground mx-1">→</span>
            <span>{props.change.to}</span>
          </span>
        )}
        {props.kind === 'scalar' && (
          <span>
            <span>{props.change.from || none}</span>
            <span className="text-muted-foreground mx-1">→</span>
            <span>{props.change.to || none}</span>
          </span>
        )}
        {props.kind === 'set' && (
          <div className="flex flex-col gap-0.5">
            {props.change.added.map(v => (
              <div key={`+${v}`} className="text-emerald-600">+ {v}</div>
            ))}
            {props.change.removed.map(v => (
              <div key={`-${v}`} className="text-destructive">− {v}</div>
            ))}
          </div>
        )}
        {props.kind === 'healthcheck' && (
          <div className="flex flex-col gap-0.5">
            {!props.change.from && props.change.to && <div className="text-emerald-600">+ {t('compose.diff.hcAdded', 'healthcheck added')}</div>}
            {props.change.from && !props.change.to && <div className="text-destructive">− {t('compose.diff.hcRemoved', 'healthcheck removed')}</div>}
            {props.change.from && props.change.to && <div className="text-blue-600">~ {t('compose.diff.hcChanged', 'healthcheck changed')}</div>}
          </div>
        )}
      </div>
    </div>
  )
}
