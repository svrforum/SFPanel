import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import type { DockerVolume } from '@/types/api'

export function DockerVolumeUsageCard() {
  const [vols, setVols] = useState<DockerVolume[]>([])
  const [loading, setLoading] = useState(true)
  const [now] = useState(() => Date.now())

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
        <CardTitle className="text-[14px]">🐳 Docker 볼륨 사용량</CardTitle>
        <Link to="/docker/volumes" className="text-[12px] text-primary hover:underline">
          전체 보기 →
        </Link>
      </CardHeader>
      <CardContent>
        {sized.length === 0 ? (
          <div className="text-[12px] text-muted-foreground text-center py-4">
            측정된 볼륨 없음 (수집 중일 수 있음)
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
                합계: {formatBytes(total)} · {sized.length}개 볼륨
              </span>
              {newest > 0 && now > 0 && <span>{Math.round((now - newest) / 60000)}분 전 측정</span>}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
