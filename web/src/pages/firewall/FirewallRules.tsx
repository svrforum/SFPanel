import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Shield, Plus, Trash2, Loader2, Power, Pencil } from 'lucide-react'
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

interface FirewallStatus {
  active: boolean
  default_incoming: string
  default_outgoing: string
}

interface FirewallRule {
  number: number
  to: string
  action: string
  from: string
  comment: string
  v6: boolean
}

interface RuleForm {
  action: string
  port: string
  protocol: string
  from: string
  comment: string
}

const initialRuleForm: RuleForm = {
  action: 'allow',
  port: '',
  protocol: 'tcp',
  from: '',
  comment: '',
}

// Port validation: number, range (8000:8080), or service name
const PORT_REGEX = /^[a-zA-Z0-9_-]+(:[a-zA-Z0-9_-]+)?$/
// IP/CIDR validation (basic)
const IP_CIDR_REGEX = /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/

function validatePort(port: string): boolean {
  return PORT_REGEX.test(port.trim())
}

function validateFrom(from: string): boolean {
  if (!from || from === 'any') return true
  return IP_CIDR_REGEX.test(from.trim())
}

// Shared form fields component
function RuleFormFields({
  form,
  setForm,
  t,
}: {
  form: RuleForm
  setForm: (f: RuleForm) => void
  t: (key: string) => string
}) {
  const portError = form.port.trim() && !validatePort(form.port)
  const fromError = form.from.trim() && !validateFrom(form.from)

  return (
    <div className="space-y-4">
      {/* Action */}
      <div className="space-y-1.5">
        <label htmlFor="rule-action" className="text-[13px] font-medium">{t('firewall.rules.action')}</label>
        <Select value={form.action} onValueChange={(v) => setForm({ ...form, action: v })}>
          <SelectTrigger id="rule-action" className="w-full rounded-xl">
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

      {/* Port */}
      <div className="space-y-1.5">
        <label htmlFor="rule-port" className="text-[13px] font-medium">{t('firewall.rules.port')}</label>
        <Input
          id="rule-port"
          value={form.port}
          onChange={(e) => setForm({ ...form, port: e.target.value })}
          placeholder="80, 443, 8000:8080"
          className={`h-9 rounded-xl bg-secondary/50 border-0 text-[13px] ${portError ? 'ring-1 ring-[#f04452]' : ''}`}
        />
        {portError && (
          <p className="text-[11px] text-[#f04452]">{t('firewall.rules.invalidPort')}</p>
        )}
      </div>

      {/* Protocol */}
      <div className="space-y-1.5">
        <label htmlFor="rule-protocol" className="text-[13px] font-medium">{t('firewall.rules.protocol')}</label>
        <Select value={form.protocol} onValueChange={(v) => setForm({ ...form, protocol: v })}>
          <SelectTrigger id="rule-protocol" className="w-full rounded-xl">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="tcp">{t('firewall.rules.tcp')}</SelectItem>
            <SelectItem value="udp">{t('firewall.rules.udp')}</SelectItem>
            <SelectItem value="any">{t('firewall.rules.both')}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Source IP */}
      <div className="space-y-1.5">
        <label htmlFor="rule-from" className="text-[13px] font-medium">{t('firewall.rules.fromIP')}</label>
        <Input
          id="rule-from"
          value={form.from}
          onChange={(e) => setForm({ ...form, from: e.target.value })}
          placeholder={t('firewall.rules.any')}
          className={`h-9 rounded-xl bg-secondary/50 border-0 text-[13px] ${fromError ? 'ring-1 ring-[#f04452]' : ''}`}
        />
        {fromError ? (
          <p className="text-[11px] text-[#f04452]">{t('firewall.rules.invalidIP')}</p>
        ) : (
          <p className="text-[11px] text-muted-foreground">{t('firewall.rules.fromIPHint')}</p>
        )}
      </div>

      {/* Comment */}
      <div className="space-y-1.5">
        <label htmlFor="rule-comment" className="text-[13px] font-medium">{t('firewall.rules.comment')}</label>
        <Input
          id="rule-comment"
          value={form.comment}
          onChange={(e) => setForm({ ...form, comment: e.target.value })}
          placeholder=""
          className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
        />
      </div>
    </div>
  )
}

export default function FirewallRules() {
  const { t } = useTranslation()

  // Status state
  const [status, setStatus] = useState<FirewallStatus | null>(null)
  const [statusLoading, setStatusLoading] = useState(true)
  const [toggling, setToggling] = useState(false)
  const [toggleConfirmOpen, setToggleConfirmOpen] = useState(false)

  // Rules state
  const [rules, setRules] = useState<FirewallRule[]>([])
  const [rulesTotal, setRulesTotal] = useState(0)
  const [rulesLoading, setRulesLoading] = useState(true)

  // Add rule dialog
  const [addOpen, setAddOpen] = useState(false)
  const [addForm, setAddForm] = useState<RuleForm>(initialRuleForm)
  const [adding, setAdding] = useState(false)

  // Delete confirmation
  const [deleteTarget, setDeleteTarget] = useState<FirewallRule | null>(null)
  const [deleting, setDeleting] = useState(false)

  // Edit rule (delete + re-add)
  const [editTarget, setEditTarget] = useState<FirewallRule | null>(null)
  const [editForm, setEditForm] = useState<RuleForm>(initialRuleForm)
  const [editing, setEditing] = useState(false)

  const fetchStatus = useCallback(async () => {
    try {
      setStatusLoading(true)
      const data = await api.getFirewallStatus()
      setStatus(data)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      toast.error(message)
    } finally {
      setStatusLoading(false)
    }
  }, [t])

  const fetchRules = useCallback(async () => {
    try {
      setRulesLoading(true)
      const data = await api.getFirewallRules()
      setRules(data.rules || [])
      setRulesTotal(data.total)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      toast.error(message)
    } finally {
      setRulesLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchStatus()
    fetchRules()
  }, [fetchStatus, fetchRules])

  const handleToggleFirewall = async () => {
    if (!status) return
    setToggling(true)
    try {
      if (status.active) {
        await api.disableFirewall()
      } else {
        await api.enableFirewall()
      }
      await fetchStatus()
      await fetchRules()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      toast.error(message)
    } finally {
      setToggling(false)
      setToggleConfirmOpen(false)
    }
  }

  const isFormValid = (form: RuleForm): boolean => {
    if (!form.port.trim() || !validatePort(form.port)) return false
    if (form.from.trim() && !validateFrom(form.from)) return false
    return true
  }

  const handleAddRule = async () => {
    if (!isFormValid(addForm)) return
    setAdding(true)
    try {
      await api.addFirewallRule({
        action: addForm.action,
        port: addForm.port.trim(),
        protocol: addForm.protocol,
        from: addForm.from.trim() || 'any',
        to: '',
        comment: addForm.comment.trim(),
      })
      setAddOpen(false)
      setAddForm(initialRuleForm)
      await fetchRules()
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
      await api.deleteFirewallRule(deleteTarget.number)
      setDeleteTarget(null)
      await fetchRules()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      toast.error(message)
    } finally {
      setDeleting(false)
    }
  }

  const parseRuleTo = (to: string): { port: string; protocol: string } => {
    const match = to.match(/^(.+)\/(tcp|udp)$/i)
    if (match) return { port: match[1], protocol: match[2].toLowerCase() }
    // "Anywhere" or other non-port values → empty port so user must re-enter
    if (!/^\d/.test(to)) return { port: '', protocol: 'tcp' }
    return { port: to, protocol: 'tcp' }
  }

  const parseRuleAction = (action: string): string => {
    const normalized = action.toUpperCase()
    if (normalized.startsWith('ALLOW')) return 'allow'
    if (normalized.startsWith('DENY')) return 'deny'
    if (normalized.startsWith('REJECT')) return 'reject'
    if (normalized.startsWith('LIMIT')) return 'limit'
    return 'allow'
  }

  const handleOpenEdit = (rule: FirewallRule) => {
    const { port, protocol } = parseRuleTo(rule.to)
    setEditForm({
      action: parseRuleAction(rule.action),
      port,
      protocol,
      from: rule.from === 'Anywhere' ? '' : rule.from,
      comment: rule.comment || '',
    })
    setEditTarget(rule)
  }

  const handleEditRule = async () => {
    if (!editTarget || !isFormValid(editForm)) return
    setEditing(true)
    try {
      // Step 1: Delete old rule first
      await api.deleteFirewallRule(editTarget.number)
      // Step 2: Add new rule
      try {
        await api.addFirewallRule({
          action: editForm.action,
          port: editForm.port.trim(),
          protocol: editForm.protocol,
          from: editForm.from.trim() || 'any',
          to: '',
          comment: editForm.comment.trim(),
        })
      } catch (addErr: unknown) {
        // Delete succeeded but add failed — warn user
        const message = addErr instanceof Error ? addErr.message : t('common.error')
        toast.error(t('firewall.rules.editAddFailed') + ': ' + message)
      }
      setEditTarget(null)
      await fetchRules()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('common.error')
      toast.error(message)
    } finally {
      setEditing(false)
    }
  }

  const getActionStyle = (action: string) => {
    const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
    const normalized = action.toUpperCase()
    if (normalized.startsWith('ALLOW')) return `${base} bg-[#00c471]/10 text-[#00c471]`
    if (normalized.startsWith('DENY') || normalized.startsWith('REJECT')) return `${base} bg-[#f04452]/10 text-[#f04452]`
    if (normalized.startsWith('LIMIT')) return `${base} bg-[#f59e0b]/10 text-[#f59e0b]`
    return `${base} bg-secondary text-muted-foreground`
  }

  const getActionLabel = (action: string) => {
    const normalized = action.toUpperCase()
    if (normalized.startsWith('ALLOW')) return t('firewall.rules.allow')
    if (normalized.startsWith('DENY')) return t('firewall.rules.deny')
    if (normalized.startsWith('REJECT')) return t('firewall.rules.reject')
    if (normalized.startsWith('LIMIT')) return t('firewall.rules.limit')
    return action
  }

  if (statusLoading && rulesLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        {t('common.loading')}
      </div>
    )
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Status Card */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-xl bg-primary/10">
              <Shield className="h-5 w-5 text-primary" aria-hidden="true" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h3 className="text-[15px] font-semibold">{t('firewall.status.title')}</h3>
                {status && (
                  <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium ${
                    status.active
                      ? 'bg-[#00c471]/10 text-[#00c471]'
                      : 'bg-[#f04452]/10 text-[#f04452]'
                  }`}>
                    {status.active ? t('firewall.status.active') : t('firewall.status.inactive')}
                  </span>
                )}
              </div>
              {status && (
                <div className="flex items-center gap-4 mt-1">
                  <span className="text-[11px] text-muted-foreground">
                    {t('firewall.status.defaultIncoming')}: <span className="font-medium text-foreground">{status.default_incoming}</span>
                  </span>
                  <span className="text-[11px] text-muted-foreground">
                    {t('firewall.status.defaultOutgoing')}: <span className="font-medium text-foreground">{status.default_outgoing}</span>
                  </span>
                </div>
              )}
            </div>
          </div>
          <Button
            variant={status?.active ? 'destructive' : 'default'}
            size="sm"
            className="rounded-xl"
            onClick={() => setToggleConfirmOpen(true)}
            disabled={toggling || !status}
          >
            {toggling ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Power className="h-3.5 w-3.5" />
            )}
            {status?.active ? t('firewall.status.disable') : t('firewall.status.enable')}
          </Button>
        </div>
      </div>

      {/* Rules Section */}
      <div className="flex items-center justify-between">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('firewall.rules.count', { count: rulesTotal })}
        </span>
        <Button size="sm" onClick={() => setAddOpen(true)} className="rounded-xl">
          <Plus className="h-3.5 w-3.5" />
          {t('firewall.rules.addRule')}
        </Button>
      </div>

      {/* Rules Table */}
      {rules.length === 0 && !rulesLoading ? (
        <div className="bg-card rounded-2xl card-shadow p-8 text-center text-muted-foreground">
          {t('firewall.rules.noRules')}
        </div>
      ) : (
        <div className="bg-card rounded-2xl card-shadow overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-16 text-[11px]">{t('firewall.rules.number')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.rules.to')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.rules.action')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.rules.from')}</TableHead>
                <TableHead className="text-[11px]">{t('firewall.rules.comment')}</TableHead>
                <TableHead className="text-right w-24 text-[11px]">{t('common.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rules.map((rule) => (
                <TableRow key={`${rule.number}-${rule.to}-${rule.action}-${rule.from}`} className="group">
                  <TableCell className="font-mono text-xs">{rule.number}</TableCell>
                  <TableCell className="text-[13px] font-mono">{rule.to}</TableCell>
                  <TableCell>
                    <span className={getActionStyle(rule.action)}>
                      {getActionLabel(rule.action)}
                    </span>
                  </TableCell>
                  <TableCell className="text-[13px] font-mono">{rule.from}</TableCell>
                  <TableCell className="text-[13px] text-muted-foreground">{rule.comment || '-'}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        className="opacity-0 group-hover:opacity-100 transition-opacity"
                        title={t('firewall.rules.editRule')}
                        onClick={() => handleOpenEdit(rule)}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        className="opacity-0 group-hover:opacity-100 transition-opacity text-red-500 hover:text-red-600"
                        title={t('firewall.rules.deleteRule')}
                        onClick={() => setDeleteTarget(rule)}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {/* Enable/Disable Confirmation Dialog */}
      <Dialog open={toggleConfirmOpen} onOpenChange={setToggleConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{status?.active ? t('firewall.status.disable') : t('firewall.status.enable')}</DialogTitle>
            <DialogDescription>
              {status?.active
                ? t('firewall.status.disableConfirm')
                : t('firewall.status.enableConfirm')}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setToggleConfirmOpen(false)} className="rounded-xl">
              {t('common.cancel')}
            </Button>
            <Button
              variant={status?.active ? 'destructive' : 'default'}
              onClick={handleToggleFirewall}
              disabled={toggling}
              className="rounded-xl"
            >
              {toggling && <Loader2 className="h-4 w-4 animate-spin" />}
              {status?.active ? t('firewall.status.disable') : t('firewall.status.enable')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Rule Confirmation Dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('firewall.rules.deleteRule')}</DialogTitle>
            <DialogDescription>
              {t('firewall.rules.deleteConfirm', { number: deleteTarget?.number })}
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

      {/* Edit Rule Dialog */}
      <Dialog open={!!editTarget} onOpenChange={(open) => {
        if (!editing && !open) {
          setEditTarget(null)
          setEditForm(initialRuleForm)
        }
      }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t('firewall.rules.editRule')}</DialogTitle>
            <DialogDescription>
              {t('firewall.rules.editDescription', { number: editTarget?.number })}
            </DialogDescription>
          </DialogHeader>
          <RuleFormFields form={editForm} setForm={setEditForm} t={t} />
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setEditTarget(null)} disabled={editing} className="rounded-xl">
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleEditRule}
              disabled={editing || !isFormValid(editForm)}
              className="rounded-xl"
            >
              {editing && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('common.save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add Rule Dialog */}
      <Dialog open={addOpen} onOpenChange={(open) => {
        if (!adding) {
          setAddOpen(open)
          if (!open) setAddForm(initialRuleForm)
        }
      }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t('firewall.rules.addRule')}</DialogTitle>
            <DialogDescription>{t('firewall.rules.title')}</DialogDescription>
          </DialogHeader>
          <RuleFormFields form={addForm} setForm={setAddForm} t={t} />
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setAddOpen(false)} disabled={adding} className="rounded-xl">
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleAddRule}
              disabled={adding || !isFormValid(addForm)}
              className="rounded-xl"
            >
              {adding && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('firewall.rules.addRule')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
