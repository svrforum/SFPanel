import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Trash2, RefreshCw, Download } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { DockerImage } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  const value = bytes / Math.pow(1024, i)
  return `${value.toFixed(1)} ${units[i]}`
}

function shortId(id: string): string {
  return id.replace('sha256:', '').substring(0, 12)
}

function formatDate(timestamp: number): string {
  return new Date(timestamp * 1000).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

export default function DockerImages() {
  const { t } = useTranslation()
  const [images, setImages] = useState<DockerImage[]>([])
  const [loading, setLoading] = useState(true)
  const [pullDialogOpen, setPullDialogOpen] = useState(false)
  const [pullImage, setPullImage] = useState('nginx:latest')
  const [pulling, setPulling] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<DockerImage | null>(null)
  const [actionLoading, setActionLoading] = useState(false)

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
    try {
      await api.pullImage(pullImage.trim())
      toast.success(t('docker.images.pullSuccess', { name: pullImage }))
      setPullDialogOpen(false)
      setPullImage('nginx:latest')
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
    setActionLoading(true)
    try {
      await api.removeImage(deleteTarget.Id)
      toast.success(t('docker.images.deleted'))
      setDeleteTarget(null)
      await fetchImages()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.images.deleteFailed')
      toast.error(message)
    } finally {
      setActionLoading(false)
    }
  }

  const getRepoTag = (image: DockerImage): string => {
    if (image.RepoTags && image.RepoTags.length > 0) {
      return image.RepoTags[0]
    }
    return '<none>:<none>'
  }

  return (
    <div className="space-y-4 mt-4">
      <div className="flex items-center justify-between">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('docker.images.count', { count: images.length })}
        </span>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={fetchImages} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" onClick={() => setPullDialogOpen(true)}>
            <Download />
            {t('docker.images.pullImage')}
          </Button>
        </div>
      </div>

      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('docker.images.repoTag')}</TableHead>
            <TableHead>{t('docker.images.imageId')}</TableHead>
            <TableHead>{t('docker.images.size')}</TableHead>
            <TableHead>{t('common.created')}</TableHead>
            <TableHead className="text-right">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {images.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                {t('docker.images.empty')}
              </TableCell>
            </TableRow>
          )}
          {images.map((img) => (
            <TableRow key={img.Id}>
              <TableCell className="font-medium font-mono text-sm">
                {getRepoTag(img)}
              </TableCell>
              <TableCell className="text-muted-foreground font-mono text-xs">
                {shortId(img.Id)}
              </TableCell>
              <TableCell className="text-muted-foreground">{formatSize(img.Size)}</TableCell>
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
            <Input
              id="pull-image"
              placeholder="e.g., nginx:latest"
              value={pullImage}
              onChange={(e) => setPullImage(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handlePull()}
            />
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
            <Button variant="destructive" onClick={handleDelete} disabled={actionLoading}>
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
