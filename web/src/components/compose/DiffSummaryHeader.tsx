import type { DiffSummary } from '@/types/api'

interface Props {
  summary: DiffSummary
}

export function DiffSummaryHeader({ summary }: Props) {
  return (
    <div
      className="flex items-center gap-4 text-[13px] bg-secondary/50 rounded-lg px-3 py-2"
      role="status"
      aria-label={`추가 ${summary.added}, 변경 ${summary.modified}, 삭제 ${summary.removed}`}
    >
      <span className="flex items-center gap-1 text-emerald-600">
        <span className="font-mono">+</span>
        <span>추가 {summary.added}</span>
      </span>
      <span className="flex items-center gap-1 text-blue-600">
        <span className="font-mono">~</span>
        <span>변경 {summary.modified}</span>
      </span>
      <span className="flex items-center gap-1 text-destructive">
        <span className="font-mono">−</span>
        <span>삭제 {summary.removed}</span>
      </span>
    </div>
  )
}
