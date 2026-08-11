import { useState, useEffect, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { RefreshCw, ArrowUp, Folder, Loader2, AlertCircle } from 'lucide-react'
import { api } from '@/lib/api'
import { formatBytes, pathBasename, pathParent } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { DiskUsageEntry } from '@/types/api'

export default function DiskUsage() {
  const { t } = useTranslation()
  const [path, setPath] = useState('/')
  const [pathInput, setPathInput] = useState('/')
  const [data, setData] = useState<DiskUsageEntry | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (p: string) => {
    setLoading(true)
    setError(null)
    try {
      const res = await api.getDiskUsage(p, 1)
      setData(res)
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.error'))
      setData(null)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    setPathInput(path)
    load(path)
  }, [path, load])

  // Immediate children of the queried dir, largest first.
  const children = useMemo(() => {
    const c = (data?.children ?? []).filter((x) => x.path !== data?.path)
    return [...c].sort((a, b) => b.size - a.size)
  }, [data])
  const maxSize = children.length > 0 ? children[0].size : 1

  return (
    <div className="space-y-4 mt-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-[17px] font-bold">{t('disk.usage.title')}</h2>
          <p className="text-[13px] text-muted-foreground mt-0.5">{t('disk.usage.description')}</p>
        </div>
        <Button variant="outline" size="sm" className="rounded-xl gap-2" onClick={() => load(path)} disabled={loading}>
          {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
          {t('common.refresh')}
        </Button>
      </div>

      {/* Path bar */}
      <div className="flex items-center gap-2">
        <Button
          variant="outline"
          size="icon-xs"
          className="rounded-xl shrink-0"
          title={t('disk.usage.up')}
          aria-label={t('disk.usage.up')}
          disabled={path === '/' || loading}
          onClick={() => setPath(pathParent(path))}
        >
          <ArrowUp className="h-4 w-4" />
        </Button>
        <form
          className="flex-1 flex gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            const v = pathInput.trim()
            if (v.startsWith('/')) setPath(v)
          }}
        >
          <Input
            value={pathInput}
            onChange={(e) => setPathInput(e.target.value)}
            className="font-mono text-[13px] rounded-xl"
            spellCheck={false}
          />
        </form>
        {data && (
          <span className="text-[13px] font-semibold shrink-0">{formatBytes(data.size)}</span>
        )}
      </div>

      {error ? (
        <div className="flex items-center gap-2 text-[13px] text-destructive bg-destructive/10 rounded-xl p-3">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {error}
        </div>
      ) : loading && !data ? (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : children.length === 0 ? (
        <p className="text-[13px] text-muted-foreground py-8 text-center">{t('disk.usage.empty')}</p>
      ) : (
        <div className="space-y-1">
          {children.map((entry) => {
            const pct = maxSize > 0 ? (entry.size / maxSize) * 100 : 0
            return (
              <button
                key={entry.path}
                onClick={() => setPath(entry.path)}
                className="w-full flex items-center gap-3 px-3 py-2 rounded-xl hover:bg-accent transition-colors text-left group outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
              >
                <Folder className="h-4 w-4 text-primary shrink-0" />
                <span className="text-[13px] font-medium w-48 truncate shrink-0" title={entry.path}>
                  {pathBasename(entry.path)}
                </span>
                <div className="flex-1 h-2 bg-muted rounded-full overflow-hidden">
                  <div className="h-full bg-primary/70 rounded-full" style={{ width: `${pct}%` }} />
                </div>
                <span className="text-[12px] text-muted-foreground tabular-nums w-20 text-right shrink-0">
                  {formatBytes(entry.size)}
                </span>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
