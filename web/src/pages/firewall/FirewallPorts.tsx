import { useState, useEffect, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Globe, Plus, Loader2, RefreshCw, Search, ArrowUpDown, ArrowUp, ArrowDown } from 'lucide-react'
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

type SortKey = 'port' | 'process' | 'protocol' | 'address' | 'none'
type SortDir = 'asc' | 'desc'

export default function FirewallPorts() {
  const { t } = useTranslation()
  const [ports, setPorts] = useState<ListeningPort[]>([])
  const [loading, setLoading] = useState(true)
  const [addTarget, setAddTarget] = useState<ListeningPort | null>(null)
  const [ruleAction, setRuleAction] = useState<string>('allow')
  const [ruleFrom, setRuleFrom] = useState<string>('')
  const [ruleComment, setRuleComment] = useState<string>('')
  const [adding, setAdding] = useState(false)

  // Search & Sort — default: no sort (API order = ss/netstat order)
  const [search, setSearch] = useState('')
  const [sortKey, setSortKey] = useState<SortKey>('none')
  const [sortDir, setSortDir] = useState<SortDir>('asc')
  const [filterProtocol, setFilterProtocol] = useState<string>('all')

  const fetchPorts = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getListeningPorts()
      setPorts(data.ports || [])
    } catch {
      toast.error(t('firewall.ports.fetchFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchPorts()
  }, [fetchPorts])

  const handleSort = (key: SortKey) => {
    if (key === 'none') return
    if (sortKey === key) {
      // 같은 컬럼 3번째 클릭 → 정렬 해제
      if (sortDir === 'desc') {
        setSortKey('none')
        setSortDir('asc')
      } else {
        setSortDir('desc')
      }
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const getSortIcon = (key: SortKey) => {
    if (sortKey !== key) return <ArrowUpDown className="h-3 w-3 ml-1 opacity-40" />
    return sortDir === 'asc'
      ? <ArrowUp className="h-3 w-3 ml-1" />
      : <ArrowDown className="h-3 w-3 ml-1" />
  }

  const filteredAndSorted = useMemo(() => {
    let result = [...ports]

    // Filter by protocol
    if (filterProtocol !== 'all') {
      result = result.filter(p => p.protocol === filterProtocol)
    }

    // Search
    if (search.trim()) {
      const q = search.toLowerCase()
      result = result.filter(p =>
        String(p.port).includes(q) ||
        (p.process || '').toLowerCase().includes(q) ||
        p.address.toLowerCase().includes(q) ||
        p.protocol.toLowerCase().includes(q) ||
        String(p.pid).includes(q)
      )
    }

    // Sort (only if user clicked a column)
    if (sortKey !== 'none') {
      result.sort((a, b) => {
        let cmp = 0
        switch (sortKey) {
          case 'port':
            cmp = a.port - b.port
            break
          case 'process':
            cmp = (a.process || '').localeCompare(b.process || '')
            break
          case 'protocol':
            cmp = a.protocol.localeCompare(b.protocol)
            break
          case 'address':
            cmp = a.address.localeCompare(b.address)
            break
        }
        return sortDir === 'asc' ? cmp : -cmp
      })
    }

    return result
  }, [ports, search, sortKey, sortDir, filterProtocol])

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
    setRuleFrom('')
    setRuleComment('')
  }

  const handleAddRule = async () => {
    if (!addTarget) return
    setAdding(true)
    try {
      await api.addFirewallRule({
        action: ruleAction,
        port: String(addTarget.port),
        protocol: normalizeProtocol(addTarget.protocol),
        from: ruleFrom.trim() || '',
        to: '',
        comment: ruleComment.trim() || '',
      })
      toast.success(t('firewall.ports.addToUFW') + ` — ${addTarget.port}/${normalizeProtocol(addTarget.protocol)}`)
      setAddTarget(null)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
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
            {t('firewall.ports.count', { count: filteredAndSorted.length })}
          </span>
        </div>
        <Button variant="outline" size="sm" onClick={fetchPorts} disabled={loading}>
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          {t('common.refresh')}
        </Button>
      </div>

      {/* Search & Filter */}
      <div className="flex items-center gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('firewall.ports.search')}
            className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
          />
        </div>
        <Select value={filterProtocol} onValueChange={setFilterProtocol}>
          <SelectTrigger className="w-28 h-9 rounded-xl text-[13px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('firewall.ports.all')}</SelectItem>
            <SelectItem value="tcp">TCP</SelectItem>
            <SelectItem value="udp">UDP</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Ports table */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead
                className="w-24 cursor-pointer select-none"
                onClick={() => handleSort('protocol')}
              >
                <span className="inline-flex items-center text-[11px]">
                  {t('firewall.ports.protocol')}{getSortIcon('protocol')}
                </span>
              </TableHead>
              <TableHead
                className="cursor-pointer select-none"
                onClick={() => handleSort('address')}
              >
                <span className="inline-flex items-center text-[11px]">
                  {t('firewall.ports.address')}{getSortIcon('address')}
                </span>
              </TableHead>
              <TableHead
                className="w-24 cursor-pointer select-none"
                onClick={() => handleSort('port')}
              >
                <span className="inline-flex items-center text-[11px]">
                  {t('firewall.ports.port')}{getSortIcon('port')}
                </span>
              </TableHead>
              <TableHead
                className="cursor-pointer select-none"
                onClick={() => handleSort('process')}
              >
                <span className="inline-flex items-center text-[11px]">
                  {t('firewall.ports.process')}{getSortIcon('process')}
                </span>
              </TableHead>
              <TableHead className="w-20 text-[11px]">{t('firewall.ports.pid')}</TableHead>
              <TableHead className="text-right w-32 text-[11px]">{t('common.actions')}</TableHead>
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
            {!loading && filteredAndSorted.length === 0 && ports.length > 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                  {t('firewall.ports.noPorts')}
                </TableCell>
              </TableRow>
            )}
            {filteredAndSorted.map((port, idx) => (
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
      <Dialog open={!!addTarget} onOpenChange={(open) => { if (!open) setAddTarget(null) }}>
        <DialogContent className="sm:max-w-md">
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
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.rules.action')}</label>
              <Select value={ruleAction} onValueChange={setRuleAction}>
                <SelectTrigger className="rounded-xl">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="allow">{t('firewall.rules.allow')}</SelectItem>
                  <SelectItem value="deny">{t('firewall.rules.deny')}</SelectItem>
                  <SelectItem value="reject">{t('firewall.rules.reject')}</SelectItem>
                  <SelectItem value="limit">{t('firewall.rules.limit')}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {/* Source IP */}
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.rules.fromIP')}</label>
              <Input
                value={ruleFrom}
                onChange={(e) => setRuleFrom(e.target.value)}
                placeholder={t('firewall.rules.any')}
                className="rounded-xl text-[13px]"
              />
              <p className="text-[11px] text-muted-foreground">{t('firewall.rules.fromIPHint')}</p>
            </div>

            {/* Comment */}
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.rules.comment')}</label>
              <Input
                value={ruleComment}
                onChange={(e) => setRuleComment(e.target.value)}
                placeholder=""
                className="rounded-xl text-[13px]"
              />
            </div>
          </div>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setAddTarget(null)} className="rounded-xl">
              {t('common.cancel')}
            </Button>
            <Button onClick={handleAddRule} disabled={adding} className="rounded-xl">
              {adding && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('firewall.rules.addRule')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
