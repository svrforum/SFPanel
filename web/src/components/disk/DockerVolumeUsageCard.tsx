import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Container } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import type { DockerVolume } from '@/types/api'

export function DockerVolumeUsageCard() {
  const { t } = useTranslation()
  const [vols, setVols] = useState<DockerVolume[]>([])
  const [loading, setLoading] = useState(true)
  // Reference time for the "measured N min ago" label. Ticks every minute
  // (while the tab is visible) so a long-open tab doesn't show a stale figure
  // frozen at mount time.
  const [now, setNow] = useState(() => Date.now())
  useVisibleInterval(() => setNow(Date.now()), 60_000)

  useEffect(() => {
    let cancelled = false
    api
      .getVolumes()
      .then((data) => {
        if (!cancelled) setVols(data ?? [])
      })
      .catch(() => {
        if (!cancelled) setVols([])
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  if (loading) return null

  const sized = vols.filter((v) => typeof v.size_bytes === 'number' && v.size_bytes !== null && v.size_bytes >= 0)
  const sorted = [...sized].sort((a, b) => (b.size_bytes ?? 0) - (a.size_bytes ?? 0))
  const top10 = sorted.slice(0, 10)
  const total = sized.reduce((s, v) => s + (v.size_bytes ?? 0), 0)
  const newest = sized.reduce((m, v) => Math.max(m, v.size_measured_at ?? 0), 0)

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-[14px] flex items-center gap-1.5">
          <Container className="h-4 w-4 text-primary" aria-hidden="true" />
          {t('disk.volumeCard.title')}
        </CardTitle>
        <Link
          to="/docker/volumes"
          className="text-[12px] text-primary hover:underline outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
        >
          {t('disk.volumeCard.viewAll')}
        </Link>
      </CardHeader>
      <CardContent>
        {sized.length === 0 ? (
          <div className="text-[12px] text-muted-foreground text-center py-4">
            {t('disk.volumeCard.empty')}
          </div>
        ) : (
          <>
            <div className="space-y-1 text-[12px]">
              {top10.map((v) => (
                <div key={v.Name} className="flex justify-between">
                  <span className="truncate flex-1 mr-2">{v.Name}</span>
                  <span className="font-mono text-muted-foreground">{formatBytes(v.size_bytes ?? 0)}</span>
                </div>
              ))}
            </div>
            <div className="mt-2 pt-2 border-t text-[11px] text-muted-foreground flex justify-between">
              <span>
                {t('disk.volumeCard.total', { size: formatBytes(total), count: sized.length })}
              </span>
              {newest > 0 && <span>{t('disk.volumeCard.measuredAgo', { minutes: Math.round((now - newest) / 60000) })}</span>}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
