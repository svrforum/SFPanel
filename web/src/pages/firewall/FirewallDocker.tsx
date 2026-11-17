import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2, Loader2, ShieldBan, ShieldCheck, AlertTriangle, Container } from 'lucide-react'
import { toast } from 'sonner'
import { api, type ApiError } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { ListLoadState } from '@/components/ListLoadState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

interface DockerPublishedPort {
  container_name: string
  container_ip: string
  host_port: number
  container_port: number
  protocol: string
  host_ip: string
}

interface DockerUserRule {
  number: number
  port: number
  protocol: string
  source: string
  action: string
  // Tied to a container address that no published port leads to any more
  // (#112): it blocks nothing, and port is the container's, not the host's.
  stale?: boolean
}

interface AddRuleForm {
  port: string
  protocol: string
  source: string
  action: string
}

const initialForm: AddRuleForm = {
  port: '',
  protocol: 'tcp',
  source: '',
  action: 'drop',
}

export default function FirewallDocker() {
  const { t } = useTranslation()
  const confirm = useConfirm()

  const [ports, setPorts] = useState<DockerPublishedPort[]>([])
  const [rules, setRules] = useState<DockerUserRule[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  // The server's DOCKER_FIREWALL_ERROR is the one failure that means the
  // chains are missing (Docker installed but not running); any other is not.
  const [chainMissing, setChainMissing] = useState(false)
  // False on a host without Docker, where neither chain exists.
  const [dockerInstalled, setDockerInstalled] = useState(true)

  // Add rule dialog
  const [addOpen, setAddOpen] = useState(false)
  const [addForm, setAddForm] = useState<AddRuleForm>(initialForm)
  const [adding, setAdding] = useState(false)
  const [addTried, setAddTried] = useState(false)

  const fetchData = useCallback(async () => {
    try {
      setLoading(true)
      setError(null)
      const data = await api.getDockerFirewall()
      setPorts(data.ports || [])
      setRules(data.rules || [])
      setDockerInstalled(data.docker_installed !== false)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      setError(message)
      setChainMissing((err as ApiError)?.code === 'DOCKER_FIREWALL_ERROR')
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  const isPortBlocked = (port: number, protocol: string) => {
    return rules.some(
      (r) => !r.stale && r.port === port && r.protocol === protocol && r.action === 'drop'
    )
  }

  const openAdd = (form: AddRuleForm) => {
    setAddForm(form)
    setAddTried(false)
    setAddOpen(true)
  }

  const handleOpenAddForPort = (port: DockerPublishedPort) => {
    openAdd({
      port: String(port.host_port),
      protocol: port.protocol,
      source: '',
      action: 'drop',
    })
  }

  // Inline validation for the add-rule port field (1-65535). An empty field
  // says so once Add was pressed; the button no longer greys out unexplained.
  const addPortNum = parseInt(addForm.port)
  const addPortInvalid = addForm.port.trim() !== ''
    ? isNaN(addPortNum) || addPortNum < 1 || addPortNum > 65535
    : addTried

  const handleAddRule = async () => {
    setAddTried(true)
    const portNum = parseInt(addForm.port)
    if (isNaN(portNum) || portNum < 1 || portNum > 65535) {
      document.getElementById('docker-rule-port')?.focus()
      return
    }

    setAdding(true)
    try {
      await api.addDockerUserRule({
        port: portNum,
        protocol: addForm.protocol,
        source: addForm.source.trim(),
        action: addForm.action,
      })
      setAddOpen(false)
      setAddForm(initialForm)
      await fetchData()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      toast.error(message)
    } finally {
      setAdding(false)
    }
  }

  const handleDeleteRule = async (rule: DockerUserRule) => {
    if (!(await confirm({
      title: t('firewall.docker.deleteTitle', { number: rule.number }),
      description: t('firewall.docker.deleteConfirm', {
        port: rule.port,
        protocol: rule.protocol.toUpperCase(),
        source: rule.source || t('firewall.rules.any'),
        action: rule.action === 'drop' ? t('firewall.docker.drop') : t('firewall.docker.accept'),
      }),
      confirmLabel: t('firewall.docker.deleteRule'),
      danger: true,
    }))) return
    try {
      await api.deleteDockerUserRule(rule.number)
      await fetchData()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      toast.error(message)
    }
  }

  // Pieces shared by the tables and their phone cards.
  const portState = (blocked: boolean) => blocked ? (
    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-medium bg-destructive/10 text-destructive">
      {t('firewall.docker.blocked')}
    </span>
  ) : (
    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-medium bg-success/10 text-success">
      {t('firewall.docker.open')}
    </span>
  )

  const blockButton = (port: DockerPublishedPort, visibility: string) => (
    <Button
      variant="ghost"
      size="sm"
      className={`text-[12px] h-7 rounded-lg ${visibility}`}
      onClick={() => handleOpenAddForPort(port)}
    >
      <ShieldBan className="h-3.5 w-3.5 mr-1" />
      {t('firewall.docker.blockPort')}
    </Button>
  )

  const ruleAction = (rule: DockerUserRule) => (
    <>
      <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-medium ${
        rule.action === 'drop'
          ? 'bg-destructive/10 text-destructive'
          : 'bg-success/10 text-success'
      }`}>
        {rule.action === 'drop' ? t('firewall.docker.drop') : t('firewall.docker.accept')}
      </span>
      {rule.stale && (
        <span
          className="ml-1.5 inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[12px] font-medium bg-warning/10 text-warning"
          title={t('firewall.docker.staleHint')}
        >
          <AlertTriangle className="h-3 w-3" />
          {t('firewall.docker.stale')}
        </span>
      )}
    </>
  )

  const deleteButton = (rule: DockerUserRule, visibility: string) => (
    <Button
      variant="ghost"
      size="icon-xs"
      className={`text-destructive hover:text-destructive/80 ${visibility}`}
      title={t('firewall.docker.deleteRule')}
      aria-label={t('firewall.docker.deleteRule')}
      onClick={() => handleDeleteRule(rule)}
    >
      <Trash2 className="h-4 w-4" />
    </Button>
  )

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        {t('common.loading')}
      </div>
    )
  }

  if (error) {
    // Surface the real failure (permissions, remote node, …): the chain is
    // named as missing only when the server says that is what failed.
    return (
      <div className="mt-4">
        <ListLoadState
          loading={false}
          error={error}
          errorTitle={chainMissing ? t('firewall.docker.noDockerUserChain') : t('firewall.docker.loadError')}
          onRetry={fetchData}
        />
      </div>
    )
  }

  if (!dockerInstalled) {
    return (
      <div className="bg-card rounded-2xl card-shadow p-8 text-center text-muted-foreground mt-4">
        <Container className="h-8 w-8 mx-auto mb-2 opacity-40" />
        <p className="text-[13px]">{t('firewall.docker.notInstalled')}</p>
      </div>
    )
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Header */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex items-center gap-3">
          <div className="p-2 rounded-xl bg-primary/10">
            <ShieldBan className="h-5 w-5 text-primary" />
          </div>
          <div>
            <span className="text-[15px] font-semibold">{t('firewall.docker.title')}</span>
            <p className="text-[12px] text-muted-foreground mt-0.5">
              {t('firewall.docker.description')}
            </p>
          </div>
        </div>
      </div>

      {/* Published Ports Section */}
      <div className="flex items-center justify-between">
        <span className="text-[15px] font-semibold">{t('firewall.docker.publishedPorts')}</span>
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {ports.length}
        </span>
      </div>

      {ports.length === 0 ? (
        <div className="bg-card rounded-2xl card-shadow p-8 text-center text-muted-foreground">
          {t('firewall.docker.noPublishedPorts')}
        </div>
      ) : (
        <div className="bg-card rounded-2xl card-shadow overflow-hidden">
          {/* A table from md, cards below it: on a phone the state and the
              Block button were past the screen's edge. */}
          <Table className="hidden md:table">
            <TableHeader>
              <TableRow>
                <TableHead className="text-[12px]">{t('firewall.docker.hostPort')}</TableHead>
                <TableHead className="text-[12px]">{t('firewall.docker.protocol')}</TableHead>
                <TableHead className="text-[12px]">{t('firewall.docker.container')}</TableHead>
                <TableHead className="text-[12px]">{t('firewall.docker.hostIP')}</TableHead>
                <TableHead className="text-[12px]">{t('common.status')}</TableHead>
                <TableHead className="text-right w-24 text-[12px]">{t('common.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ports.map((port) => {
                const blocked = isPortBlocked(port.host_port, port.protocol)
                return (
                  <TableRow key={`${port.host_port}-${port.protocol}-${port.container_ip}`} className="group">
                    <TableCell className="font-mono text-[13px] font-medium">{port.host_port}</TableCell>
                    <TableCell className="text-[13px] uppercase">{port.protocol}</TableCell>
                    <TableCell className="text-[13px]">
                      <span className="font-medium">{port.container_name}</span>
                      <span className="text-muted-foreground">:{port.container_port}</span>
                    </TableCell>
                    <TableCell className="text-[13px] font-mono text-muted-foreground">{port.host_ip}</TableCell>
                    <TableCell>{portState(blocked)}</TableCell>
                    <TableCell className="text-right">
                      {!blocked && blockButton(port, 'opacity-100 md:can-hover:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100 transition-opacity')}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
          <ul className="md:hidden divide-y divide-border">
            {ports.map((port) => {
              const blocked = isPortBlocked(port.host_port, port.protocol)
              return (
                <li key={`${port.host_port}-${port.protocol}-${port.container_ip}`} className="flex items-center gap-2 p-3">
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-[13px] font-medium">{port.host_port}</span>
                      <span className="text-[12px] uppercase text-muted-foreground">{port.protocol}</span>
                      {portState(blocked)}
                    </div>
                    <p className="text-[12px] text-muted-foreground break-all">
                      <span className="font-medium text-foreground">{port.container_name}</span>:{port.container_port}
                      {' · '}
                      <span className="font-mono">{port.host_ip}</span>
                    </p>
                  </div>
                  {!blocked && blockButton(port, 'shrink-0')}
                </li>
              )
            })}
          </ul>
        </div>
      )}

      {/* DOCKER-USER Rules Section */}
      <div className="flex items-center justify-between">
        <span className="text-[15px] font-semibold">{t('firewall.docker.userRules')}</span>
        <div className="flex items-center gap-2">
          <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
            {t('firewall.docker.rulesCount', { count: rules.length })}
          </span>
          <Button size="sm" onClick={() => openAdd(initialForm)} className="rounded-xl">
            <Plus className="h-3.5 w-3.5" />
            {t('firewall.docker.addRule')}
          </Button>
        </div>
      </div>

      {rules.length === 0 ? (
        <div className="bg-card rounded-2xl card-shadow p-8 text-center text-muted-foreground">
          <ShieldCheck className="h-8 w-8 mx-auto mb-2 opacity-40" />
          {t('firewall.docker.noRules')}
        </div>
      ) : (
        <div className="space-y-3">
          {rules.some((r) => r.stale) && (
            <div role="alert" className="flex items-start gap-2 rounded-xl border border-warning/30 bg-warning/10 p-3 text-[13px] text-warning">
              <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" />
              <span>{t('firewall.docker.staleBanner', { count: rules.filter((r) => r.stale).length })}</span>
            </div>
          )}
          <div className="bg-card rounded-2xl card-shadow overflow-hidden">
            <Table className="hidden md:table">
              <TableHeader>
                <TableRow>
                  <TableHead className="w-16 text-[12px]">#</TableHead>
                  <TableHead className="text-[12px]">{t('firewall.docker.port')}</TableHead>
                  <TableHead className="text-[12px]">{t('firewall.docker.protocol')}</TableHead>
                  <TableHead className="text-[12px]">{t('firewall.docker.source')}</TableHead>
                  <TableHead className="text-[12px]">{t('firewall.docker.action')}</TableHead>
                  <TableHead className="text-right w-16 text-[12px]">{t('common.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rules.map((rule) => (
                  <TableRow key={rule.number} className="group">
                    <TableCell className="font-mono text-xs">{rule.number}</TableCell>
                    <TableCell className="font-mono text-[13px] font-medium">{rule.port}</TableCell>
                    <TableCell className="text-[13px] uppercase">{rule.protocol}</TableCell>
                    <TableCell className="text-[13px] font-mono">
                      {rule.source || <span className="text-muted-foreground">*</span>}
                    </TableCell>
                    <TableCell>{ruleAction(rule)}</TableCell>
                    <TableCell className="text-right">
                      {deleteButton(rule, 'opacity-100 md:can-hover:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100 transition-opacity')}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <ul className="md:hidden divide-y divide-border">
              {rules.map((rule) => (
                <li key={rule.number} className="flex items-center gap-2 p-3">
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-[13px] font-medium">{rule.port}</span>
                      <span className="text-[12px] uppercase text-muted-foreground">{rule.protocol}</span>
                      {ruleAction(rule)}
                    </div>
                    <p className="text-[12px] text-muted-foreground break-all">
                      #{rule.number} · {t('firewall.docker.source')} <span className="font-mono">{rule.source || '*'}</span>
                    </p>
                  </div>
                  {deleteButton(rule, 'shrink-0')}
                </li>
              ))}
            </ul>
          </div>
        </div>
      )}

      {/* Add Rule Dialog */}
      <Dialog open={addOpen} onOpenChange={(open) => { if (!adding) setAddOpen(open) }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t('firewall.docker.addRuleTitle')}</DialogTitle>
            <DialogDescription>{t('firewall.docker.addRuleDesc')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            {/* Port */}
            <div className="space-y-1.5">
              <label htmlFor="docker-rule-port" className="text-[13px] font-medium">{t('firewall.docker.port')}</label>
              <Input
                id="docker-rule-port"
                type="number"
                value={addForm.port}
                onChange={(e) => setAddForm({ ...addForm, port: e.target.value })}
                placeholder="80"
                aria-invalid={addPortInvalid || undefined}
                aria-describedby={addPortInvalid ? 'docker-rule-port-error' : undefined}
                className="rounded-xl text-base md:text-[13px]"
              />
              {addPortInvalid && (
                <p id="docker-rule-port-error" className="text-[12px] text-destructive">{t('firewall.docker.invalidPort')}</p>
              )}
            </div>

            {/* Protocol */}
            <div className="space-y-1.5">
              <label htmlFor="docker-rule-protocol" className="text-[13px] font-medium">{t('firewall.docker.protocol')}</label>
              <Select value={addForm.protocol} onValueChange={(v) => setAddForm({ ...addForm, protocol: v })}>
                <SelectTrigger id="docker-rule-protocol" className="w-full rounded-xl">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="tcp">TCP</SelectItem>
                  <SelectItem value="udp">UDP</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {/* Source IP */}
            <div className="space-y-1.5">
              <label htmlFor="docker-rule-source" className="text-[13px] font-medium">{t('firewall.docker.source')}</label>
              <Input
                id="docker-rule-source"
                value={addForm.source}
                onChange={(e) => setAddForm({ ...addForm, source: e.target.value })}
                placeholder="192.168.1.0/24"
                aria-describedby="docker-rule-source-hint"
                className="rounded-xl text-base md:text-[13px]"
              />
              <p id="docker-rule-source-hint" className="text-[12px] text-muted-foreground">{t('firewall.docker.sourceHint')}</p>
            </div>

            {/* Action */}
            <div className="space-y-1.5">
              <label htmlFor="docker-rule-action" className="text-[13px] font-medium">{t('firewall.docker.action')}</label>
              <Select value={addForm.action} onValueChange={(v) => setAddForm({ ...addForm, action: v })}>
                <SelectTrigger id="docker-rule-action" className="w-full rounded-xl">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="drop">{t('firewall.docker.drop')}</SelectItem>
                  <SelectItem value="accept">{t('firewall.docker.accept')}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setAddOpen(false)} disabled={adding} className="rounded-xl">
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleAddRule}
              disabled={adding}
              className="rounded-xl"
            >
              {adding && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('firewall.docker.addRule')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
