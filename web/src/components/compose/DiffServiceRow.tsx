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
  return (
    <div className="grid grid-cols-[120px_1fr] gap-2 py-1 text-[12px]">
      <span className="font-medium truncate" title={props.change.service}>{props.change.service}</span>
      <div className="font-mono leading-relaxed">
        {props.kind === 'image' && (
          <span>
            <span>{props.change.from || '(없음)'}</span>
            <span className="text-muted-foreground mx-1">→</span>
            <span>{props.change.to}</span>
          </span>
        )}
        {props.kind === 'scalar' && (
          <span>
            <span>{props.change.from || '(없음)'}</span>
            <span className="text-muted-foreground mx-1">→</span>
            <span>{props.change.to || '(없음)'}</span>
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
            {!props.change.from && props.change.to && <div className="text-emerald-600">+ healthcheck 추가</div>}
            {props.change.from && !props.change.to && <div className="text-destructive">− healthcheck 제거</div>}
            {props.change.from && props.change.to && <div className="text-blue-600">~ healthcheck 변경</div>}
          </div>
        )}
      </div>
    </div>
  )
}
