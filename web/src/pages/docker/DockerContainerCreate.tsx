import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Plus, Trash2, ChevronDown, ChevronUp } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import DockerHubSearch from '@/components/DockerHubSearch'

interface PortRow { host: string; container: string; protocol: string }
interface VolumeRow { host: string; container: string }
interface EnvRow { key: string; value: string }

/**
 * Split a command string respecting single and double quotes.
 * e.g., `/bin/sh -c 'echo hello world'` -> ['/bin/sh', '-c', 'echo hello world']
 */
function splitCommand(cmd: string): string[] {
  const args: string[] = []
  let current = ''
  let inSingle = false
  let inDouble = false

  for (let i = 0; i < cmd.length; i++) {
    const ch = cmd[i]
    if (ch === "'" && !inDouble) {
      inSingle = !inSingle
    } else if (ch === '"' && !inSingle) {
      inDouble = !inDouble
    } else if (ch === ' ' && !inSingle && !inDouble) {
      if (current) {
        args.push(current)
        current = ''
      }
    } else {
      current += ch
    }
  }
  if (current) args.push(current)
  return args
}

export default function DockerContainerCreate() {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const [imageName, setImageName] = useState('')
  const [containerName, setContainerName] = useState('')
  const [ports, setPorts] = useState<PortRow[]>([{ host: '', container: '', protocol: 'tcp' }])
  const [volumes, setVolumes] = useState<VolumeRow[]>([{ host: '', container: '' }])
  const [envVars, setEnvVars] = useState<EnvRow[]>([{ key: '', value: '' }])
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [restartPolicy, setRestartPolicy] = useState('no')
  const [memoryLimit, setMemoryLimit] = useState('')
  const [networkMode, setNetworkMode] = useState('')
  const [hostname, setHostname] = useState('')
  const [command, setCommand] = useState('')
  const [creating, setCreating] = useState(false)

  const addPort = () => setPorts([...ports, { host: '', container: '', protocol: 'tcp' }])
  const removePort = (i: number) => setPorts(ports.filter((_, idx) => idx !== i))
  const updatePort = (i: number, field: keyof PortRow, val: string) => {
    const copy = [...ports]; copy[i] = { ...copy[i], [field]: val }; setPorts(copy)
  }

  const addVolume = () => setVolumes([...volumes, { host: '', container: '' }])
  const removeVolume = (i: number) => setVolumes(volumes.filter((_, idx) => idx !== i))
  const updateVolume = (i: number, field: keyof VolumeRow, val: string) => {
    const copy = [...volumes]; copy[i] = { ...copy[i], [field]: val }; setVolumes(copy)
  }

  const addEnv = () => setEnvVars([...envVars, { key: '', value: '' }])
  const removeEnv = (i: number) => setEnvVars(envVars.filter((_, idx) => idx !== i))
  const updateEnv = (i: number, field: keyof EnvRow, val: string) => {
    const copy = [...envVars]; copy[i] = { ...copy[i], [field]: val }; setEnvVars(copy)
  }

  const handleCreate = async (autoStart: boolean) => {
    if (!imageName.trim()) {
      toast.error(t('docker.containerCreate.imagePlaceholder'))
      return
    }

    const portsMap: Record<string, string> = {}
    for (const p of ports) {
      if (p.host && p.container) {
        portsMap[`${p.container}/${p.protocol}`] = p.host
      }
    }

    const volumesMap: Record<string, string> = {}
    for (const v of volumes) {
      if (v.host && v.container) {
        volumesMap[v.host] = v.container
      }
    }

    const envList: string[] = []
    for (const e of envVars) {
      if (e.key) envList.push(`${e.key}=${e.value}`)
    }

    setCreating(true)
    try {
      await api.createContainer({
        name: containerName.trim(),
        image: imageName.trim(),
        ports: Object.keys(portsMap).length > 0 ? portsMap : undefined,
        volumes: Object.keys(volumesMap).length > 0 ? volumesMap : undefined,
        env: envList.length > 0 ? envList : undefined,
        restart_policy: restartPolicy,
        memory_limit: memoryLimit ? parseInt(memoryLimit) * 1024 * 1024 : undefined,
        network_mode: networkMode || undefined,
        hostname: hostname || undefined,
        cmd: command.trim() ? splitCommand(command.trim()) : undefined,
        auto_start: autoStart,
      })
      toast.success(t('docker.containerCreate.createSuccess'))
      navigate('/docker/containers')
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.containerCreate.createFailed'))
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="space-y-6 max-w-2xl">
      <h2 className="text-[18px] font-bold">{t('docker.containerCreate.title')}</h2>

      {/* Image */}
      <div className="space-y-2">
        <Label>{t('docker.containerCreate.imageName')}</Label>
        <DockerHubSearch value={imageName} onChange={setImageName} placeholder={t('docker.containerCreate.imagePlaceholder')} />
      </div>

      {/* Container name */}
      <div className="space-y-2">
        <Label>{t('docker.containerCreate.containerName')}</Label>
        <Input value={containerName} onChange={(e) => setContainerName(e.target.value)}
          placeholder={t('docker.containerCreate.containerNamePlaceholder')}
          className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]" />
      </div>

      {/* Ports */}
      <div className="space-y-2">
        <Label>{t('docker.containerCreate.portMapping')}</Label>
        <div className="space-y-2">
          {ports.map((p, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input value={p.host} onChange={(e) => updatePort(i, 'host', e.target.value)}
                placeholder={t('docker.containerCreate.hostPort')}
                className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] w-24" />
              <span className="text-muted-foreground text-sm">:</span>
              <Input value={p.container} onChange={(e) => updatePort(i, 'container', e.target.value)}
                placeholder={t('docker.containerCreate.containerPort')}
                className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] w-24" />
              <select value={p.protocol} onChange={(e) => updatePort(i, 'protocol', e.target.value)}
                className="h-9 rounded-xl bg-secondary/50 border-0 px-2 text-[13px]">
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
              {ports.length > 1 && (
                <Button variant="ghost" size="icon-xs" onClick={() => removePort(i)}><Trash2 className="h-3.5 w-3.5" /></Button>
              )}
            </div>
          ))}
          <Button variant="outline" size="sm" onClick={addPort} className="rounded-xl text-[13px]">
            <Plus className="h-3.5 w-3.5" />{t('docker.containerCreate.addPort')}
          </Button>
        </div>
      </div>

      {/* Volumes */}
      <div className="space-y-2">
        <Label>{t('docker.containerCreate.volumeMapping')}</Label>
        <div className="space-y-2">
          {volumes.map((v, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input value={v.host} onChange={(e) => updateVolume(i, 'host', e.target.value)}
                placeholder={t('docker.containerCreate.hostPath')}
                className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] flex-1" />
              <span className="text-muted-foreground text-sm">:</span>
              <Input value={v.container} onChange={(e) => updateVolume(i, 'container', e.target.value)}
                placeholder={t('docker.containerCreate.containerPath')}
                className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] flex-1" />
              {volumes.length > 1 && (
                <Button variant="ghost" size="icon-xs" onClick={() => removeVolume(i)}><Trash2 className="h-3.5 w-3.5" /></Button>
              )}
            </div>
          ))}
          <Button variant="outline" size="sm" onClick={addVolume} className="rounded-xl text-[13px]">
            <Plus className="h-3.5 w-3.5" />{t('docker.containerCreate.addVolume')}
          </Button>
        </div>
      </div>

      {/* Env vars */}
      <div className="space-y-2">
        <Label>{t('docker.containerCreate.envVars')}</Label>
        <div className="space-y-2">
          {envVars.map((e, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input value={e.key} onChange={(ev) => updateEnv(i, 'key', ev.target.value)}
                placeholder={t('docker.containerCreate.envKey')}
                className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] w-40" />
              <span className="text-muted-foreground text-sm">=</span>
              <Input value={e.value} onChange={(ev) => updateEnv(i, 'value', ev.target.value)}
                placeholder={t('docker.containerCreate.envValue')}
                className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] flex-1" />
              {envVars.length > 1 && (
                <Button variant="ghost" size="icon-xs" onClick={() => removeEnv(i)}><Trash2 className="h-3.5 w-3.5" /></Button>
              )}
            </div>
          ))}
          <Button variant="outline" size="sm" onClick={addEnv} className="rounded-xl text-[13px]">
            <Plus className="h-3.5 w-3.5" />{t('docker.containerCreate.addEnv')}
          </Button>
        </div>
      </div>

      {/* Advanced */}
      <div>
        <button
          className="flex items-center gap-1.5 text-[13px] text-muted-foreground hover:text-foreground transition-colors"
          onClick={() => setShowAdvanced(!showAdvanced)}
        >
          {showAdvanced ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
          {t('docker.containerCreate.advanced')}
        </button>
        {showAdvanced && (
          <div className="mt-3 space-y-4 pl-1">
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label>{t('docker.containerCreate.restartPolicy')}</Label>
                <select value={restartPolicy} onChange={(e) => setRestartPolicy(e.target.value)}
                  className="flex h-9 w-full rounded-xl border-0 bg-secondary/50 px-3 py-1 text-[13px]">
                  <option value="no">no</option>
                  <option value="always">always</option>
                  <option value="unless-stopped">unless-stopped</option>
                  <option value="on-failure">on-failure</option>
                </select>
              </div>
              <div className="space-y-2">
                <Label>{t('docker.containerCreate.memoryLimit')}</Label>
                <Input type="number" value={memoryLimit} onChange={(e) => setMemoryLimit(e.target.value)}
                  placeholder="0 (unlimited)"
                  className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]" />
              </div>
              <div className="space-y-2">
                <Label>{t('docker.containerCreate.networkMode')}</Label>
                <Input value={networkMode} onChange={(e) => setNetworkMode(e.target.value)}
                  placeholder="bridge"
                  className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]" />
              </div>
              <div className="space-y-2">
                <Label>{t('docker.containerCreate.hostname')}</Label>
                <Input value={hostname} onChange={(e) => setHostname(e.target.value)}
                  className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]" />
              </div>
            </div>
            <div className="space-y-2">
              <Label>{t('docker.containerCreate.command')}</Label>
              <Input value={command} onChange={(e) => setCommand(e.target.value)}
                placeholder="e.g., /bin/sh -c 'echo hello'"
                className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]" />
            </div>
          </div>
        )}
      </div>

      {/* Actions */}
      <div className="flex items-center gap-3 pt-2">
        <Button variant="outline" onClick={() => navigate('/docker/containers')} className="rounded-xl">
          {t('common.cancel')}
        </Button>
        <Button variant="outline" onClick={() => handleCreate(false)} disabled={creating || !imageName.trim()} className="rounded-xl">
          {t('docker.containerCreate.create')}
        </Button>
        <Button onClick={() => handleCreate(true)} disabled={creating || !imageName.trim()} className="rounded-xl">
          {t('docker.containerCreate.createAndStart')}
        </Button>
      </div>
    </div>
  )
}
