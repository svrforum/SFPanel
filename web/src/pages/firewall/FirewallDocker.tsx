import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2, Loader2, ShieldBan, ShieldCheck } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
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

  const [ports, setPorts] = useState<DockerPublishedPort[]>([])
  const [rules, setRules] = useState<DockerUserRule[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Add rule dialog
  const [addOpen, setAddOpen] = useState(false)
  const [addForm, setAddForm] = useState<AddRuleForm>(initialForm)
  const [adding, setAdding] = useState(false)

  // Delete confirmation
  const [deleteTarget, setDeleteTarget] = useState<DockerUserRule | null>(null)
  const [deleting, setDeleting] = useState(false)

  const fetchData = useCallback(async () => {
    try {
      setLoading(true)
      setError(null)
      const data = await api.getDockerFirewall()
      setPorts(data.ports || [])
      setRules(data.rules || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      setError(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  const isPortBlocked = (port: number, protocol: string) => {
    return rules.some(
      (r) => r.port === port && r.protocol === protocol && r.action === 'drop'
    )
  }

  const handleOpenAddForPort = (port: DockerPublishedPort) => {
    setAddForm({
      port: String(port.host_port),
      protocol: port.protocol,
      source: '',
      action: 'drop',
    })
    setAddOpen(true)
  }

  const handleAddRule = async () => {
    const portNum = parseInt(addForm.port)
    if (isNaN(portNum) || portNum < 1 || portNum > 65535) return

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

  const handleDeleteRule = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await api.deleteDockerUserRule(deleteTarget.number)
      setDeleteTarget(null)
      await fetchData()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      toast.error(message)
    } finally {
      setDeleting(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        {t('common.loading')}
      </div>
    )
  }

  if (error) {
    return (
      <div className="bg-card rounded-2xl card-shadow p-8 text-center text-muted-foreground mt-4">
        <p>{t('firewall.docker.noDockerUserChain')}</p>
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
            <p className="text-[11px] text-muted-foreground mt-0.5">
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
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="text-[11px]">{t('firewall.docker.hostPort')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.docker.protocol')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.docker.container')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.docker.hostIP')}</TableHead>
                <TableHead className="text-[11px]">{t('common.status')}</TableHead>
                <TableHead className="text-right w-24 text-[11px]">{t('common.actions')}</TableHead>
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
                    <TableCell>
                      {blocked ? (
                        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#f04452]/10 text-[#f04452]">
                          {t('firewall.docker.blocked')}
                        </span>
                      ) : (
                        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                          {t('firewall.docker.open')}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      {!blocked && (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="opacity-0 group-hover:opacity-100 transition-opacity text-[12px] h-7 rounded-lg"
                          onClick={() => handleOpenAddForPort(port)}
                        >
                          <ShieldBan className="h-3.5 w-3.5 mr-1" />
                          {t('firewall.docker.blockPort')}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}

      {/* DOCKER-USER Rules Section */}
      <div className="flex items-center justify-between">
        <span className="text-[15px] font-semibold">{t('firewall.docker.userRules')}</span>
        <div className="flex items-center gap-2">
          <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
            {t('firewall.docker.rulesCount', { count: rules.length })}
          </span>
          <Button size="sm" onClick={() => { setAddForm(initialForm); setAddOpen(true) }} className="rounded-xl">
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
        <div className="bg-card rounded-2xl card-shadow overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-16 text-[11px]">#</TableHead>
                <TableHead className="text-[11px]">{t('firewall.docker.port')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.docker.protocol')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.docker.source')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.docker.action')}</TableHead>
                <TableHead className="text-right w-16 text-[11px]">{t('common.actions')}</TableHead>
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
                  <TableCell>
                    <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium ${
                      rule.action === 'drop'
                        ? 'bg-[#f04452]/10 text-[#f04452]'
                        : 'bg-[#00c471]/10 text-[#00c471]'
                    }`}>
                      {rule.action === 'drop' ? t('firewall.docker.drop') : t('firewall.docker.accept')}
                    </span>
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      className="opacity-0 group-hover:opacity-100 transition-opacity text-red-500 hover:text-red-600"
                      title={t('firewall.docker.deleteRule')}
                      onClick={() => setDeleteTarget(rule)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
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
              <label className="text-[13px] font-medium">{t('firewall.docker.port')}</label>
              <Input
                type="number"
                value={addForm.port}
                onChange={(e) => setAddForm({ ...addForm, port: e.target.value })}
                placeholder="80"
                className="rounded-xl text-[13px]"
              />
            </div>

            {/* Protocol */}
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.docker.protocol')}</label>
              <Select value={addForm.protocol} onValueChange={(v) => setAddForm({ ...addForm, protocol: v })}>
                <SelectTrigger className="w-full rounded-xl">
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
              <label className="text-[13px] font-medium">{t('firewall.docker.source')}</label>
              <Input
                value={addForm.source}
                onChange={(e) => setAddForm({ ...addForm, source: e.target.value })}
                placeholder="192.168.1.0/24"
                className="rounded-xl text-[13px]"
              />
              <p className="text-[11px] text-muted-foreground">{t('firewall.docker.sourceHint')}</p>
            </div>

            {/* Action */}
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.docker.action')}</label>
              <Select value={addForm.action} onValueChange={(v) => setAddForm({ ...addForm, action: v })}>
                <SelectTrigger className="w-full rounded-xl">
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
              disabled={adding || !addForm.port}
              className="rounded-xl"
            >
              {adding && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('firewall.docker.addRule')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Rule Confirmation Dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('firewall.docker.deleteRule')}</DialogTitle>
            <DialogDescription>
              {t('firewall.docker.deleteConfirm')}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setDeleteTarget(null)} className="rounded-xl">
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleDeleteRule}
              disabled={deleting}
              className="rounded-xl"
            >
              {deleting && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
