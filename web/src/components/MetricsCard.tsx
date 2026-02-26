interface MetricsCardProps {
  title: string
  value: string
  percent: number
  icon: React.ReactNode
}

function getBarColor(percent: number): string {
  if (percent > 80) return 'bg-[#f04452]'
  if (percent >= 60) return 'bg-[#f59e0b]'
  return 'bg-primary'
}

export default function MetricsCard({ title, value, percent, icon }: MetricsCardProps) {
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
    </div>
  )
}
