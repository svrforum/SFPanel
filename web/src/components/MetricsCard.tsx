interface MetricsCardProps {
  title: string
  value: string
  percent: number
  icon: React.ReactNode
  subLabel?: string
  subValue?: string
  subPercent?: number
}

function getBarColor(percent: number): string {
  if (percent > 80) return 'bg-[#f04452]'
  if (percent >= 60) return 'bg-[#f59e0b]'
  return 'bg-primary'
}

export default function MetricsCard({ title, value, percent, icon, subLabel, subValue, subPercent }: MetricsCardProps) {
  const clampedPercent = Math.min(100, Math.max(0, percent))

  return (
    <div className="bg-card rounded-2xl p-5 card-shadow">
      <div className="flex items-center gap-2.5 mb-4">
        <div className="text-primary/70">{icon}</div>
        <span className="text-[13px] font-medium text-muted-foreground">{title}</span>
      </div>
      <div className="text-xl font-bold tracking-tight mb-3">{value}</div>
      {clampedPercent > 0 && (
        <div className="w-full h-1.5 bg-secondary rounded-full overflow-hidden">
          <div
            className={`h-full rounded-full transition-all duration-700 ease-out ${getBarColor(clampedPercent)}`}
            style={{ width: `${clampedPercent}%` }}
          />
        </div>
      )}
      {subLabel && subValue && (
        <div className="mt-3 pt-3 border-t border-border">
          <div className="flex items-center justify-between mb-1.5">
            <span className="text-[11px] text-muted-foreground">{subLabel}</span>
            <span className="text-[12px] font-semibold">{subValue}</span>
          </div>
          {subPercent !== undefined && subPercent > 0 && (
            <div className="w-full h-1 bg-secondary rounded-full overflow-hidden">
              <div
                className={`h-full rounded-full transition-all duration-700 ease-out ${getBarColor(Math.min(100, subPercent))}`}
                style={{ width: `${Math.min(100, subPercent)}%` }}
              />
            </div>
          )}
        </div>
      )}
    </div>
  )
}
