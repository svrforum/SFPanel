import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Trash2, RefreshCw, Download, Sparkles, Check, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes, formatDate } from '@/lib/utils'
import DockerHubSearch from '@/components/DockerHubSearch'
import type { DockerImage, ImageUpdateStatus } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

function shortId(id: string): string {
  return id.replace('sha256:', '').substring(0, 12)
}

export default function DockerImages() {
  const { t } = useTranslation()
  const [images, setImages] = useState<DockerImage[]>([])
  const [loading, setLoading] = useState(true)
  const [pullDialogOpen, setPullDialogOpen] = useState(false)
  const [pullImage, setPullImage] = useState('nginx:latest')
  const [pulling, setPulling] = useState(false)
  const [pullProgress, setPullProgress] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<DockerImage | null>(null)
  const [actionLoading, setActionLoading] = useState<string | null>(null)
  const [pruning, setPruning] = useState(false)
  const [pruneConfirmOpen, setPruneConfirmOpen] = useState(false)
  const [updateResults, setUpdateResults] = useState<ImageUpdateStatus[]>([])
  const [checkingUpdates, setCheckingUpdates] = useState(false)

  const fetchImages = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getImages()
      setImages(data || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.images.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchImages()
  }, [fetchImages])

  const handlePull = async () => {
    if (!pullImage.trim()) return
    setPulling(true)
    setPullProgress('')
    try {
      await api.pullImageStream(pullImage.trim(), (event) => {
        const progress = event.id
          ? `[${event.id}] ${event.status}${event.progress ? ' ' + event.progress : ''}`
          : event.status + (event.progress ? ' ' + event.progress : '')
        setPullProgress(progress)
      })
      toast.success(t('docker.images.pullSuccess', { name: pullImage }))
      setPullDialogOpen(false)
      setPullImage('nginx:latest')
      setPullProgress('')
      await fetchImages()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.images.pullFailed')
      toast.error(message)
    } finally {
      setPulling(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setActionLoading(deleteTarget.Id)
    try {
      await api.removeImage(deleteTarget.Id)
      toast.success(t('docker.images.deleted'))
      setDeleteTarget(null)
      await fetchImages()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.images.deleteFailed')
      toast.error(message)
    } finally {
      setActionLoading(null)
    }
  }

  const getRepoTag = (image: DockerImage): string => {
    if (image.RepoTags && image.RepoTags.length > 0) {
      return image.RepoTags[0]
    }
    return '<none>:<none>'
  }

  const handleCheckUpdates = async () => {
    setCheckingUpdates(true)
    try {
      const results = await api.checkImageUpdates()
      setUpdateResults(results)
      const updateCount = results.filter(r => r.has_update).length
      if (updateCount > 0) {
        toast.info(t('docker.images.updatesFound', { count: updateCount }))
      } else {
        toast.success(t('docker.images.upToDate'))
      }
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.images.updateCheckFailed'))
    } finally {
      setCheckingUpdates(false)
    }
  }

  const getUpdateStatus = (imageName: string) => {
    return updateResults.find(r => r.image === imageName)
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between flex-wrap gap-2">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary shrink-0">
          {t('docker.images.count', { count: images.length })}
        </span>
        <div className="flex items-center gap-2 overflow-x-auto no-scrollbar">
          <Button variant="outline" size="sm" className="shrink-0" onClick={() => setPruneConfirmOpen(true)} disabled={pruning}>
            <Sparkles className={pruning ? 'animate-spin' : ''} />
            {t('docker.sidebar.prune')}
          </Button>
          <Button variant="outline" size="sm" className="rounded-xl shrink-0"
            onClick={handleCheckUpdates} disabled={checkingUpdates}>
            {checkingUpdates ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
            {t('docker.images.checkUpdates')}
          </Button>
          <Button variant="outline" size="sm" className="shrink-0" onClick={fetchImages} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" className="shrink-0" onClick={() => setPullDialogOpen(true)}>
            <Download />
            {t('docker.images.pullImage')}
          </Button>
        </div>
      </div>

      {/* Mobile card view */}
      <div className="md:hidden space-y-2">
        {images.length === 0 && !loading && (
          <div className="text-center text-muted-foreground py-8 text-[13px]">
            {t('docker.images.empty')}
          </div>
        )}
        {[...images].sort((a, b) => (a.in_use === b.in_use ? 0 : a.in_use ? -1 : 1)).map((img) => (
          <div key={img.Id} className="bg-card rounded-2xl p-4 card-shadow">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0 flex-1">
                <p className="text-[13px] font-medium font-mono truncate">{getRepoTag(img)}</p>
                <div className="flex items-center gap-2 mt-1">
                  <span className="text-[11px] text-muted-foreground font-mono">{shortId(img.Id)}</span>
                  <span className="text-[11px] text-muted-foreground">{formatBytes(img.Size)}</span>
                </div>
                <div className="flex items-center gap-1.5 mt-1.5 flex-wrap">
                  {img.in_use ? (
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                      {t('docker.images.inUse')}
                    </span>
                  ) : (
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">
                      {t('docker.images.unused')}
                    </span>
                  )}
                  {getUpdateStatus(img.RepoTags?.[0] || '')?.has_update && (
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#3182f6]/10 text-[#3182f6]">
                      {t('docker.images.updateAvailable')}
                    </span>
                  )}
                </div>
              </div>
              <Button
                variant="ghost"
                size="icon-xs"
                title={t('common.delete')}
                onClick={() => setDeleteTarget(img)}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          </div>
        ))}
      </div>

      {/* Desktop table */}
      <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('docker.images.repoTag')}</TableHead>
            <TableHead>{t('docker.images.imageId')}</TableHead>
            <TableHead>{t('common.status')}</TableHead>
            <TableHead>{t('docker.images.size')}</TableHead>
            <TableHead>{t('common.created')}</TableHead>
            <TableHead className="text-right">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {images.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                {t('docker.images.empty')}
              </TableCell>
            </TableRow>
          )}
          {[...images].sort((a, b) => (a.in_use === b.in_use ? 0 : a.in_use ? -1 : 1)).map((img) => (
            <TableRow key={img.Id}>
              <TableCell className="font-medium font-mono text-sm">
                <div className="flex items-center gap-1.5">
                  {img.in_use && <Check className="h-3.5 w-3.5 text-[#00c471] shrink-0" />}
                  {getRepoTag(img)}
                </div>
              </TableCell>
              <TableCell className="text-muted-foreground font-mono text-xs">
                {shortId(img.Id)}
              </TableCell>
              <TableCell>
                <div className="flex items-center gap-1.5 flex-wrap">
                  {img.in_use ? (
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]" title={img.used_by.join(', ')}>
                      {t('docker.images.inUse')}
                    </span>
                  ) : (
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">
                      {t('docker.images.unused')}
                    </span>
                  )}
                  {getUpdateStatus(img.RepoTags?.[0] || '')?.has_update && (
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#3182f6]/10 text-[#3182f6]">
                      {t('docker.images.updateAvailable')}
                    </span>
                  )}
                </div>
              </TableCell>
              <TableCell className="text-muted-foreground">{formatBytes(img.Size)}</TableCell>
              <TableCell className="text-muted-foreground">{formatDate(img.Created)}</TableCell>
              <TableCell className="text-right">
                <Button
                    variant="ghost"
                    size="icon-xs"
                    title={t('common.delete')}
                    onClick={() => setDeleteTarget(img)}
                  >
                    <Trash2 />
                  </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      </div>

      {/* Pull image dialog */}
      <Dialog open={pullDialogOpen} onOpenChange={setPullDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.images.pullImage')}</DialogTitle>
            <DialogDescription>
              {t('docker.images.pullDescription')}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="pull-image">{t('docker.images.imageReference')}</Label>
            <DockerHubSearch
              value={pullImage}
              onChange={setPullImage}
              placeholder="e.g., nginx:latest"
            />
            {pulling && pullProgress && (
              <div className="mt-2 px-3 py-2 rounded-xl bg-secondary/50 text-[12px] font-mono text-muted-foreground truncate">
                {pullProgress}
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPullDialogOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handlePull} disabled={pulling || !pullImage.trim()}>
              {pulling ? t('docker.images.pulling') : t('docker.images.pull')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Prune confirmation dialog */}
      <Dialog open={pruneConfirmOpen} onOpenChange={setPruneConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.prune.title')}</DialogTitle>
            <DialogDescription>{t('docker.prune.imagesConfirm')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPruneConfirmOpen(false)}>{t('common.cancel')}</Button>
            <Button variant="destructive" disabled={pruning} onClick={async () => {
              setPruning(true)
              try {
                const r = await api.pruneImages()
                toast.success(t('docker.prune.success') + (r.deleted > 0 ? `: ${r.deleted} deleted` : ''))
                fetchImages()
              } catch (err: unknown) { toast.error(err instanceof Error ? err.message : 'Prune failed') }
              finally { setPruning(false); setPruneConfirmOpen(false) }
            }}>
              {pruning ? t('docker.prune.pruning') : t('docker.prune.confirm')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.images.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="docker.images.deleteConfirm"
                values={{ name: deleteTarget ? getRepoTag(deleteTarget) : '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleDelete} disabled={!!actionLoading}>
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
