import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronRight, Plus, Loader2, X } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { CreateContainerSpec, PortBindingSpec, DockerNetwork } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Label } from '@/components/ui/label'

// Row shapes for the create-container form's dynamic lists
interface PortRow { hostPort: string; containerPort: string; protocol: 'tcp' | 'udp' }
interface EnvRow { key: string; value: string }
interface VolumeRow { hostPath: string; containerPath: string; readOnly: boolean }

export function CreateContainerDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const { t } = useTranslation()
  const [image, setImage] = useState('')
  const [name, setName] = useState('')
  const [command, setCommand] = useState('')
  const [ports, setPorts] = useState<PortRow[]>([])
  const [envs, setEnvs] = useState<EnvRow[]>([])
  const [volumes, setVolumes] = useState<VolumeRow[]>([])
  const [restartPolicy, setRestartPolicy] = useState('no')
  const [network, setNetwork] = useState('')
  const [autoStart, setAutoStart] = useState(true)
  const [networks, setNetworks] = useState<DockerNetwork[]>([])
  const [submitting, setSubmitting] = useState(false)

  const resetForm = useCallback(() => {
    setImage('')
    setName('')
    setCommand('')
    setPorts([])
    setEnvs([])
    setVolumes([])
    setRestartPolicy('no')
    setNetwork('')
    setAutoStart(true)
  }, [])

  // Fetch networks when the dialog opens
  useEffect(() => {
    if (!open) return
    let cancelled = false
    api.getNetworks()
      .then((data) => { if (!cancelled) setNetworks(data || []) })
      .catch(() => { /* network list is non-critical */ })
    return () => { cancelled = true }
  }, [open])

  const handleSubmit = async () => {
    const trimmedImage = image.trim()
    if (!trimmedImage) {
      toast.error(t('docker.create.imageRequired'))
      return
    }

    const spec: CreateContainerSpec = { image: trimmedImage }

    const trimmedName = name.trim()
    if (trimmedName) spec.name = trimmedName

    const cmdParts = command.trim().split(/\s+/).filter(Boolean)
    if (cmdParts.length > 0) spec.command = cmdParts

    const portSpecs: PortBindingSpec[] = ports
      .filter((p) => p.containerPort.trim() !== '')
      .map((p) => {
        const binding: PortBindingSpec = { container_port: p.containerPort.trim(), protocol: p.protocol }
        if (p.hostPort.trim() !== '') binding.host_port = p.hostPort.trim()
        return binding
      })
    if (portSpecs.length > 0) spec.ports = portSpecs

    const envSpecs = envs
      .filter((e) => e.key.trim() !== '')
      .map((e) => `${e.key.trim()}=${e.value}`)
    if (envSpecs.length > 0) spec.env = envSpecs

    const volumeSpecs = volumes
      .filter((v) => v.hostPath.trim() !== '' && v.containerPath.trim() !== '')
      .map((v) => `${v.hostPath.trim()}:${v.containerPath.trim()}${v.readOnly ? ':ro' : ''}`)
    if (volumeSpecs.length > 0) spec.volumes = volumeSpecs

    if (restartPolicy && restartPolicy !== 'no') spec.restart_policy = restartPolicy
    if (network) spec.network = network
    spec.auto_start = autoStart

    setSubmitting(true)
    try {
      const res = await api.createContainer(spec)
      toast.success(t('docker.create.success', { id: res.id.substring(0, 12) }))
      resetForm()
      onOpenChange(false)
      onCreated()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!submitting) onOpenChange(o) }}>
      <DialogContent className="sm:max-w-2xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t('docker.create.title')}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {/* Image */}
          <div className="space-y-1.5">
            <Label className="text-[13px]">{t('docker.create.image')}</Label>
            <Input
              value={image}
              onChange={(e) => setImage(e.target.value)}
              placeholder={t('docker.create.imagePlaceholder')}
              className="h-9 rounded-xl text-[13px]"
            />
          </div>

          {/* Name */}
          <div className="space-y-1.5">
            <Label className="text-[13px]">{t('docker.create.name')}</Label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('docker.create.namePlaceholder')}
              className="h-9 rounded-xl text-[13px]"
            />
          </div>

          {/* Ports */}
          <div className="space-y-1.5">
            <Label className="text-[13px]">{t('docker.create.ports')}</Label>
            <div className="space-y-2">
              {ports.map((p, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Input
                    value={p.hostPort}
                    onChange={(e) => setPorts((prev) => prev.map((r, j) => j === i ? { ...r, hostPort: e.target.value } : r))}
                    placeholder={t('docker.create.hostPort')}
                    className="h-9 rounded-xl text-[13px]"
                  />
                  <ChevronRight className="h-4 w-4 text-muted-foreground shrink-0" />
                  <Input
                    value={p.containerPort}
                    onChange={(e) => setPorts((prev) => prev.map((r, j) => j === i ? { ...r, containerPort: e.target.value } : r))}
                    placeholder={t('docker.create.containerPort')}
                    className="h-9 rounded-xl text-[13px]"
                  />
                  <Select
                    value={p.protocol}
                    onValueChange={(v) => setPorts((prev) => prev.map((r, j) => j === i ? { ...r, protocol: v as 'tcp' | 'udp' } : r))}
                  >
                    <SelectTrigger className="w-24 h-9 rounded-xl text-[13px] shrink-0">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="tcp">TCP</SelectItem>
                      <SelectItem value="udp">UDP</SelectItem>
                    </SelectContent>
                  </Select>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    className="shrink-0"
                    title={t('common.delete')}
                    aria-label={t('common.delete')}
                    onClick={() => setPorts((prev) => prev.filter((_, j) => j !== i))}
                  >
                    <X />
                  </Button>
                </div>
              ))}
              <Button
                variant="outline"
                size="sm"
                className="rounded-xl"
                onClick={() => setPorts((prev) => [...prev, { hostPort: '', containerPort: '', protocol: 'tcp' }])}
              >
                <Plus />
                {t('docker.create.addPort')}
              </Button>
            </div>
          </div>

          {/* Environment variables */}
          <div className="space-y-1.5">
            <Label className="text-[13px]">{t('docker.create.env')}</Label>
            <div className="space-y-2">
              {envs.map((e, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Input
                    value={e.key}
                    onChange={(ev) => setEnvs((prev) => prev.map((r, j) => j === i ? { ...r, key: ev.target.value } : r))}
                    placeholder={t('docker.create.key')}
                    className="h-9 rounded-xl text-[13px]"
                  />
                  <span className="text-muted-foreground shrink-0">=</span>
                  <Input
                    value={e.value}
                    onChange={(ev) => setEnvs((prev) => prev.map((r, j) => j === i ? { ...r, value: ev.target.value } : r))}
                    placeholder={t('docker.create.value')}
                    className="h-9 rounded-xl text-[13px]"
                  />
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    className="shrink-0"
                    title={t('common.delete')}
                    aria-label={t('common.delete')}
                    onClick={() => setEnvs((prev) => prev.filter((_, j) => j !== i))}
                  >
                    <X />
                  </Button>
                </div>
              ))}
              <Button
                variant="outline"
                size="sm"
                className="rounded-xl"
                onClick={() => setEnvs((prev) => [...prev, { key: '', value: '' }])}
              >
                <Plus />
                {t('docker.create.addEnv')}
              </Button>
            </div>
          </div>

          {/* Volumes */}
          <div className="space-y-1.5">
            <Label className="text-[13px]">{t('docker.create.volumes')}</Label>
            <div className="space-y-2">
              {volumes.map((v, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Input
                    value={v.hostPath}
                    onChange={(e) => setVolumes((prev) => prev.map((r, j) => j === i ? { ...r, hostPath: e.target.value } : r))}
                    placeholder={t('docker.create.hostPath')}
                    className="h-9 rounded-xl text-[13px]"
                  />
                  <ChevronRight className="h-4 w-4 text-muted-foreground shrink-0" />
                  <Input
                    value={v.containerPath}
                    onChange={(e) => setVolumes((prev) => prev.map((r, j) => j === i ? { ...r, containerPath: e.target.value } : r))}
                    placeholder={t('docker.create.containerPath')}
                    className="h-9 rounded-xl text-[13px]"
                  />
                  <label className="flex items-center gap-1.5 text-[13px] text-muted-foreground shrink-0 cursor-pointer">
                    <Checkbox
                      checked={v.readOnly}
                      onCheckedChange={(c) => setVolumes((prev) => prev.map((r, j) => j === i ? { ...r, readOnly: c === true } : r))}
                    />
                    {t('docker.create.readOnly')}
                  </label>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    className="shrink-0"
                    title={t('common.delete')}
                    aria-label={t('common.delete')}
                    onClick={() => setVolumes((prev) => prev.filter((_, j) => j !== i))}
                  >
                    <X />
                  </Button>
                </div>
              ))}
              <Button
                variant="outline"
                size="sm"
                className="rounded-xl"
                onClick={() => setVolumes((prev) => [...prev, { hostPath: '', containerPath: '', readOnly: false }])}
              >
                <Plus />
                {t('docker.create.addVolume')}
              </Button>
            </div>
          </div>

          {/* Restart policy + Network */}
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label className="text-[13px]">{t('docker.create.restartPolicy')}</Label>
              <Select value={restartPolicy} onValueChange={setRestartPolicy}>
                <SelectTrigger className="h-9 rounded-xl text-[13px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="no">no</SelectItem>
                  <SelectItem value="always">always</SelectItem>
                  <SelectItem value="unless-stopped">unless-stopped</SelectItem>
                  <SelectItem value="on-failure">on-failure</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label className="text-[13px]">{t('docker.create.network')}</Label>
              <Select value={network || '__default__'} onValueChange={(v) => setNetwork(v === '__default__' ? '' : v)}>
                <SelectTrigger className="h-9 rounded-xl text-[13px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__default__">{t('docker.create.networkDefault')}</SelectItem>
                  {networks.map((n) => (
                    <SelectItem key={n.Name} value={n.Name}>{n.Name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          {/* Command */}
          <div className="space-y-1.5">
            <Label className="text-[13px]">{t('docker.create.command')}</Label>
            <Input
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder={t('docker.create.commandPlaceholder')}
              className="h-9 rounded-xl text-[13px]"
            />
          </div>

          {/* Auto-start */}
          <label className="flex items-center gap-2 text-[13px] cursor-pointer">
            <Checkbox checked={autoStart} onCheckedChange={(c) => setAutoStart(c === true)} />
            {t('docker.create.autoStart')}
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)} disabled={submitting}>
            {t('common.cancel')}
          </Button>
          <Button className="rounded-xl" onClick={handleSubmit} disabled={submitting}>
            {submitting ? <Loader2 className="animate-spin" /> : <Plus />}
            {submitting ? t('docker.create.creating') : t('docker.create.submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
