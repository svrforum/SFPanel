import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Globe, Plus, Loader2, RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
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
  DialogFooter,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { ListeningPort } from '@/types/api'

export default function FirewallPorts() {
  const { t } = useTranslation()
  const [ports, setPorts] = useState<ListeningPort[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [addTarget, setAddTarget] = useState<ListeningPort | null>(null)
  const [ruleAction, setRuleAction] = useState<string>('allow')
  const [adding, setAdding] = useState(false)

  const fetchPorts = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getListeningPorts()
      setPorts(data.ports || [])
      setTotal(data.total)
    } catch {
      toast.error(t('firewall.ports.fetchFailed', t('common.loading')))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchPorts()
  }, [fetchPorts])

  const getProtocolStyle = (protocol: string) => {
    const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
    if (protocol.startsWith('tcp')) {
      return `${base} bg-[#3182f6]/10 text-[#3182f6]`
    }
    if (protocol.startsWith('udp')) {
      return `${base} bg-[#f59e0b]/10 text-[#f59e0b]`
    }
    return `${base} bg-secondary text-muted-foreground`
  }

  const normalizeProtocol = (protocol: string): string => {
    if (protocol.startsWith('tcp')) return 'tcp'
    if (protocol.startsWith('udp')) return 'udp'
    return 'tcp'
  }

  const handleOpenAddDialog = (port: ListeningPort) => {
    setAddTarget(port)
    setRuleAction('allow')
  }

  const handleAddRule = async () => {
    if (!addTarget) return
    setAdding(true)
    try {
      await api.addFirewallRule({
        action: ruleAction,
        port: String(addTarget.port),
        protocol: normalizeProtocol(addTarget.protocol),
        from: '',
        to: '',
        comment: '',
      })
      toast.success(t('firewall.ports.addToUFW') + ` — ${addTarget.port}/${normalizeProtocol(addTarget.protocol)}`)
      setAddTarget(null)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.loading')
      toast.error(message)
    } finally {
      setAdding(false)
    }
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h2 className="text-[15px] font-semibold flex items-center gap-2">
            <Globe className="h-4 w-4" />
            {t('firewall.ports.title')}
          </h2>
          <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
            {t('firewall.ports.count', { count: total })}
          </span>
        </div>
        <Button variant="outline" size="sm" onClick={fetchPorts} disabled={loading}>
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          {t('common.refresh')}
        </Button>
      </div>

      {/* Ports table */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-24">{t('firewall.ports.protocol')}</TableHead>
              <TableHead>{t('firewall.ports.address')}</TableHead>
              <TableHead className="w-24">{t('firewall.ports.port')}</TableHead>
              <TableHead>{t('firewall.ports.process')}</TableHead>
              <TableHead className="w-20">{t('firewall.ports.pid')}</TableHead>
              <TableHead className="text-right w-32">{t('common.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {ports.length === 0 && !loading && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                  {t('firewall.ports.noPorts')}
                </TableCell>
              </TableRow>
            )}
            {loading && ports.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                  <Loader2 className="h-5 w-5 animate-spin mx-auto" />
                </TableCell>
              </TableRow>
            )}
            {ports.map((port, idx) => (
              <TableRow key={`${port.protocol}-${port.address}-${port.port}-${idx}`} className="group">
                <TableCell>
                  <span className={getProtocolStyle(port.protocol)}>
                    {port.protocol.toUpperCase()}
                  </span>
                </TableCell>
                <TableCell className="text-[13px] font-mono">{port.address}</TableCell>
                <TableCell className="text-[13px] font-mono font-medium">{port.port}</TableCell>
                <TableCell className="text-[13px]">{port.process || '—'}</TableCell>
                <TableCell className="text-[13px] font-mono text-muted-foreground">
                  {port.pid > 0 ? port.pid : '—'}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 text-xs opacity-0 group-hover:opacity-100 transition-opacity"
                    onClick={() => handleOpenAddDialog(port)}
                  >
                    <Plus className="h-3.5 w-3.5 mr-1" />
                    {t('firewall.ports.addToUFW')}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Add to UFW Dialog */}
      <Dialog open={!!addTarget} onOpenChange={(open) => !open && setAddTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('firewall.ports.addToUFW')}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            {/* Port info */}
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
                  {t('firewall.rules.port')}
                </label>
                <p className="text-[13px] font-mono font-medium mt-1">{addTarget?.port}</p>
              </div>
              <div>
                <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
                  {t('firewall.rules.protocol')}
                </label>
                <p className="text-[13px] font-mono font-medium mt-1">
                  {addTarget ? normalizeProtocol(addTarget.protocol).toUpperCase() : ''}
                </p>
              </div>
            </div>

            {/* Action selector */}
            <div>
              <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
                {t('firewall.rules.action')}
              </label>
              <Select value={ruleAction} onValueChange={setRuleAction}>
                <SelectTrigger className="mt-1 rounded-xl">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="allow">{t('firewall.rules.allow')}</SelectItem>
                  <SelectItem value="deny">{t('firewall.rules.deny')}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAddTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleAddRule} disabled={adding}>
              {adding && <Loader2 className="h-4 w-4 animate-spin mr-1" />}
              {t('firewall.rules.addRule')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
