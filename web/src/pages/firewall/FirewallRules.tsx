import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Shield, Plus, Trash2, Loader2, Power, Pencil, RefreshCw, AlertCircle, ExternalLink } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
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
import { useConfirm } from '@/components/ConfirmDialog'
import { normalizePort, portNeedsProtocol, validatePort, validateFrom } from './ruleValidation'
import { useLockoutGuard } from './useLockoutGuard'

interface FirewallStatus {
  active: boolean
  default_incoming: string
  default_outgoing: string
  // Absent from older nodes, which always managed ufw.
  installed?: boolean
  manager?: 'ufw' | 'pve-firewall' | 'none'
  pve?: { enabled: boolean; running: boolean; raw: string }
}

// The Proxmox web UI of the host this page shows. A remote cluster node's
// address is not known here, so it is only offered for the node the browser
// reached.
function proxmoxUiUrl(): string | null {
  if (api.currentNode) return null
  try {
    const host = api.serverUrl ? new URL(api.serverUrl).hostname : window.location.hostname
    return `https://${host}:8006`
  } catch {
    return null
  }
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

// Shared form fields component
function RuleFormFields({
  form,
  setForm,
}: {
  form: RuleForm
  setForm: (f: RuleForm) => void
}) {
  const { t } = useTranslation()
  const portError = form.port.trim() && !validatePort(form.port)
  const protocolError = !portError && form.port.trim() && form.protocol === 'any' && portNeedsProtocol(form.port)
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
          className={`h-9 rounded-xl bg-secondary/50 border-0 text-[13px] ${portError ? 'ring-1 ring-destructive' : ''}`}
        />
        {portError && (
          <p className="text-[11px] text-destructive">{t('firewall.rules.invalidPort')}</p>
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
        {protocolError && (
          <p className="text-[11px] text-destructive">{t('firewall.rules.portNeedsProtocol')}</p>
        )}
      </div>

      {/* Source IP */}
      <div className="space-y-1.5">
        <label htmlFor="rule-from" className="text-[13px] font-medium">{t('firewall.rules.fromIP')}</label>
        <Input
          id="rule-from"
          value={form.from}
          onChange={(e) => setForm({ ...form, from: e.target.value })}
          placeholder={t('firewall.rules.any')}
          className={`h-9 rounded-xl bg-secondary/50 border-0 text-[13px] ${fromError ? 'ring-1 ring-destructive' : ''}`}
        />
        {fromError ? (
          <p className="text-[11px] text-destructive">{t('firewall.rules.invalidIP')}</p>
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
  const confirm = useConfirm()

  // Status state
  const [status, setStatus] = useState<FirewallStatus | null>(null)
  const [statusLoading, setStatusLoading] = useState(true)
  const [toggling, setToggling] = useState(false)
  // Lockout-guard override: opened when the backend refuses an enable/add/delete
  // with 409 because it would block SSH/panel or cluster access. retry() re-runs with force.
  const { handleLockoutError, clearLockout, lockoutDialog } = useLockoutGuard()

  // Rules state
  const [rules, setRules] = useState<FirewallRule[]>([])
  const [rulesTotal, setRulesTotal] = useState(0)
  const [rulesLoading, setRulesLoading] = useState(true)
  const [rulesError, setRulesError] = useState<string | null>(null)

  // Add rule dialog
  const [addOpen, setAddOpen] = useState(false)
  const [addForm, setAddForm] = useState<RuleForm>(initialRuleForm)
  const [adding, setAdding] = useState(false)

  // Rule number currently being deleted (disables that row's delete button)
  const [deleting, setDeleting] = useState<number | null>(null)

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
      setRulesError(null)
      const data = await api.getFirewallRules()
      setRules(data.rules || [])
      setRulesTotal(data.total)
    } catch (err: unknown) {
      setRulesError(err instanceof Error ? err.message : String(err))
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

  const runToggleFirewall = async (force: boolean) => {
    if (!status) return
    setToggling(true)
    try {
      if (status.active) {
        await api.disableFirewall()
      } else {
        await api.enableFirewall(force)
      }
      await fetchStatus()
      await fetchRules()
      clearLockout()
    } catch (err: unknown) {
      if (!handleLockoutError(err, () => runToggleFirewall(true))) {
        toast.error(err instanceof Error ? err.message : t('common.error'))
      }
    } finally {
      setToggling(false)
    }
  }
  const handleToggleFirewall = async () => {
    if (!status) return
    if (!(await confirm({
      title: status.active ? t('firewall.status.disable') : t('firewall.status.enable'),
      description: status.active
        ? t('firewall.status.disableConfirm')
        : t('firewall.status.enableConfirm'),
      confirmLabel: status.active ? t('firewall.status.disable') : t('firewall.status.enable'),
      danger: status.active,
    }))) return
    await runToggleFirewall(false)
  }

  const isFormValid = (form: RuleForm): boolean => {
    if (!form.port.trim() || !validatePort(form.port)) return false
    if (form.protocol === 'any' && portNeedsProtocol(form.port)) return false
    if (form.from.trim() && !validateFrom(form.from)) return false
    return true
  }

  const runAddRule = async (force: boolean) => {
    if (!isFormValid(addForm)) return
    setAdding(true)
    try {
      await api.addFirewallRule({
        action: addForm.action,
        port: normalizePort(addForm.port),
        protocol: addForm.protocol,
        from: addForm.from.trim() || 'any',
        to: '',
        comment: addForm.comment.trim(),
      }, force)
      setAddOpen(false)
      setAddForm(initialRuleForm)
      await fetchRules()
      clearLockout()
    } catch (err: unknown) {
      if (!handleLockoutError(err, () => runAddRule(true))) {
        toast.error(err instanceof Error ? err.message : t('common.error'))
      }
    } finally {
      setAdding(false)
    }
  }
  const handleAddRule = () => runAddRule(false)

  const runDeleteRule = async (rule: FirewallRule, force: boolean) => {
    setDeleting(rule.number)
    try {
      await api.deleteFirewallRule(rule.number, force)
      await fetchRules()
      clearLockout()
    } catch (err: unknown) {
      if (!handleLockoutError(err, () => runDeleteRule(rule, true))) {
        toast.error(err instanceof Error ? err.message : t('common.error'))
      }
    } finally {
      setDeleting(null)
    }
  }
  const handleDeleteRule = async (rule: FirewallRule) => {
    if (!(await confirm({
      title: t('firewall.rules.deleteRule'),
      description: t('firewall.rules.deleteConfirm', { number: rule.number }),
      danger: true,
    }))) return
    await runDeleteRule(rule, false)
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
      // Add the replacement FIRST, so a failure leaves the existing rule intact.
      // (Deleting first would leave the firewall with NO rule if the add failed —
      // a real protection gap.) UFW appends, so the old rule's number is unchanged.
      await api.addFirewallRule({
        action: editForm.action,
        port: normalizePort(editForm.port),
        protocol: editForm.protocol,
        from: editForm.from.trim() || 'any',
        to: '',
        comment: editForm.comment.trim(),
      })
      // Replacement is active — now remove the old rule.
      try {
        await api.deleteFirewallRule(editTarget.number)
      } catch (delErr: unknown) {
        // New rule is in place; the old one just couldn't be removed. Surface it
        // so the operator can delete the leftover duplicate manually.
        const message = delErr instanceof Error ? delErr.message : t('common.error')
        toast.warning(t('firewall.rules.editDeleteFailed') + ': ' + message)
      }
      setEditTarget(null)
      await fetchRules()
    } catch (addErr: unknown) {
      // Add failed — the original rule is untouched, so the firewall is unchanged.
      const message = addErr instanceof Error ? addErr.message : t('common.error')
      toast.error(t('firewall.rules.editAddFailed') + ': ' + message)
    } finally {
      setEditing(false)
    }
  }

  const getActionStyle = (action: string) => {
    const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
    const normalized = action.toUpperCase()
    if (normalized.startsWith('ALLOW')) return `${base} bg-success/10 text-success`
    if (normalized.startsWith('DENY') || normalized.startsWith('REJECT')) return `${base} bg-destructive/10 text-destructive`
    if (normalized.startsWith('LIMIT')) return `${base} bg-warning/10 text-warning`
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

  const manager = status?.manager ?? 'ufw'
  const installed = status?.installed !== false

  // On a Proxmox host the firewall is pve-firewall's, and the server refuses
  // ufw changes: this page only says how it stands and where to change it.
  if (status && manager === 'pve-firewall') {
    const url = proxmoxUiUrl()
    const badge = (on: boolean) =>
      `inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium ${on ? 'bg-success/10 text-success' : 'bg-secondary text-muted-foreground'}`
    return (
      <div className="space-y-4 mt-4">
        <div className="bg-card rounded-2xl p-5 card-shadow space-y-4">
          <div className="flex items-start gap-3">
            <div className="p-2 rounded-xl bg-primary/10 shrink-0">
              <Shield className="h-5 w-5 text-primary" aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <h3 className="text-[15px] font-semibold">{t('firewall.pve.title')}</h3>
              <p className="text-[12px] text-muted-foreground mt-0.5">{t('firewall.managedByPve')}</p>
            </div>
          </div>
          {status.pve && (
            <div className="space-y-2">
              <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-[12px] text-muted-foreground">
                <span className="flex items-center gap-2">
                  {t('firewall.pve.datacenter')}
                  <span className={badge(status.pve.enabled)}>
                    {status.pve.enabled ? t('firewall.pve.enabled') : t('firewall.pve.disabled')}
                  </span>
                </span>
                <span className="flex items-center gap-2">
                  {t('firewall.pve.service')}
                  <span className={badge(status.pve.running)}>
                    {status.pve.running ? t('firewall.pve.running') : t('firewall.pve.stopped')}
                  </span>
                </span>
              </div>
              {status.pve.raw && (
                <p className="font-mono text-[11px] text-muted-foreground break-words">{status.pve.raw}</p>
              )}
            </div>
          )}
          {url && (
            <Button asChild variant="outline" size="sm" className="rounded-xl">
              <a href={url} target="_blank" rel="noopener noreferrer">
                <ExternalLink className="h-3.5 w-3.5" />
                {t('firewall.pve.openUi')}
              </a>
            </Button>
          )}
        </div>
      </div>
    )
  }

  // No ufw on this host (Debian installs none): saying "Inactive" and offering
  // Enable, which could only fail, read as a firewall turned off.
  if (status && !installed) {
    return (
      <div className="space-y-4 mt-4">
        <div className="bg-card rounded-2xl p-5 card-shadow">
          <div className="flex items-start gap-3">
            <div className="p-2 rounded-xl bg-primary/10 shrink-0">
              <Shield className="h-5 w-5 text-primary" aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <h3 className="text-[15px] font-semibold">{t('firewall.status.title')}</h3>
                <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">
                  {t('firewall.status.notInstalled')}
                </span>
              </div>
              <p className="text-[12px] text-muted-foreground mt-1">{t('firewall.status.notInstalledHint')}</p>
            </div>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Status Card */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex flex-wrap items-center justify-between gap-2">
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
                      ? 'bg-success/10 text-success'
                      : 'bg-destructive/10 text-destructive'
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
            onClick={handleToggleFirewall}
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
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('firewall.rules.count', { count: rulesTotal })}
        </span>
        <Button size="sm" onClick={() => setAddOpen(true)} className="rounded-xl">
          <Plus className="h-3.5 w-3.5" />
          {t('firewall.rules.addRule')}
        </Button>
      </div>

      {/* Rules Table */}
      {rulesError && rules.length === 0 ? (
        <div className="bg-destructive/10 text-destructive rounded-xl p-3 flex items-start gap-2">
          <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-medium">{t('firewall.rules.loadError')}</p>
            <p className="text-[12px] opacity-80 mt-0.5 break-words">{rulesError}</p>
          </div>
          <Button variant="outline" size="sm" className="rounded-xl shrink-0" onClick={fetchRules}>
            <RefreshCw className="h-3.5 w-3.5" />
            {t('common.retry')}
          </Button>
        </div>
      ) : rulesLoading && rules.length === 0 ? (
        <div className="bg-card rounded-2xl p-3 card-shadow space-y-2">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-10 w-full rounded-xl" />
          ))}
        </div>
      ) : rules.length === 0 ? (
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
                  <TableCell className="text-[13px] font-mono">
                    {rule.to}
                    {rule.v6 && (
                      <span className="ml-1.5 rounded-md bg-secondary px-1.5 py-0.5 font-sans text-[10px] text-muted-foreground">
                        {t('firewall.rules.ipv6')}
                      </span>
                    )}
                  </TableCell>
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
                        className="opacity-100 md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100 transition-opacity"
                        title={t('firewall.rules.editRule')}
                        aria-label={t('firewall.rules.editRule')}
                        onClick={() => handleOpenEdit(rule)}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        className="opacity-100 md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100 transition-opacity text-red-500 hover:text-red-600"
                        title={t('firewall.rules.deleteRule')}
                        aria-label={t('firewall.rules.deleteRule')}
                        onClick={() => handleDeleteRule(rule)}
                        disabled={deleting === rule.number}
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

      {/* Lockout-guard force-override Dialog */}
      {lockoutDialog}

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
          <RuleFormFields form={editForm} setForm={setEditForm} />
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
          <RuleFormFields form={addForm} setForm={setAddForm} />
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
