import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Pencil, Trash2 } from 'lucide-react'
import { api, type ApiError } from '@/lib/api'
import { cn, formatDate } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useConfirm } from '@/components/ConfirmDialog'
import type { AgentInstallLogin } from '@/types/api'
import { EditorField } from '../TaskStepCard'
import { useMounted } from '../useMounted'
import { AccountFields } from './AccountFields'
import { formProblemText, methodText } from './installForm'
import {
  changeFromLogin, loginBody, loginChangeProblems, loginErrorText, loginNameRefusal, loginNameText, withLogin, type LoginChange,
} from './installLogins'

// The account's fields the change form shows, which a refusal is put under.
const ACCOUNT_FIELDS = new Set(['user', 'method', 'password', 'key', 'passphrase', 'sudo_same', 'sudo_password'])
// The list, or a change, takes the height the dialog has left and scrolls
// within it: the title and the buttons stay in view on a phone, the keyboard
// up or not (the dialog is at most the visible screen's height).
const SCROLLS = 'min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain'

interface InstallLoginsDialogProps {
  /** The saved logins, as the pane holds them. */
  logins: AgentInstallLogin[]
  /** The list after a change or a delete, for the pane to hold. */
  onChange: (logins: AgentInstallLogin[]) => void
  onClose: () => void
}

// The saved logins, a line each: the name, user · method, and when it last
// changed, with [Edit] and [Delete]. [Edit] changes the name, the user, the
// method and its secrets in the same fields as the install form. A secret is
// never shown: its field starts empty, and left empty it keeps the stored
// one, which its hint says only where one is stored. [Delete] asks first.
// The secrets typed live in the change's state alone and leave the browser
// in the PUT's body alone. Mounted while open.
export function InstallLoginsDialog({ logins, onChange, onClose }: InstallLoginsDialogProps) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const mounted = useMounted()
  const id = useId()
  // The login being changed, and the change: its secrets empty until typed.
  const [editing, setEditing] = useState<AgentInstallLogin | null>(null)
  const [draft, setDraft] = useState<LoginChange | null>(null)
  // The change's problems show once a save was asked for.
  const [attempted, setAttempted] = useState(false)
  // The leader's refusal of the name, or of an account field it names.
  const [refusedName, setRefusedName] = useState<string | null>(null)
  const [refusedField, setRefusedField] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const edit = (l: AgentInstallLogin) => {
    setEditing(l)
    setDraft(changeFromLogin(l))
    setAttempted(false)
    setRefusedName(null)
    setRefusedField(null)
    setError(null)
  }
  // Back to the list: what was typed goes with the change.
  const back = () => {
    setEditing(null)
    setDraft(null)
  }

  const problems = editing && draft && attempted ? loginChangeProblems(draft, editing, logins) : null
  const nameError = refusedName ?? (problems?.name ? loginNameText(problems.name, t) : undefined)
  const fieldProblem = (field: string) =>
    problems?.fields.includes(field) || refusedField === field ? formProblemText({ field }, t) : undefined

  const saveChange = async () => {
    if (!editing || !draft) return
    setAttempted(true)
    setRefusedName(null)
    setRefusedField(null)
    setError(null)
    const found = loginChangeProblems(draft, editing, logins)
    if (found.name || found.fields.length > 0) return
    setBusy(true)
    try {
      const saved = await api.updateAgentInstallLogin(editing.id, loginBody(draft.name, draft))
      onChange(withLogin(logins, saved))
      if (mounted.current) back()
    } catch (err) {
      if (!mounted.current) return
      const { code, details } = (err ?? {}) as ApiError
      const onName = loginNameRefusal(err, t)
      if (onName) {
        setRefusedName(onName)
      } else if (code === 'AGENT_INSTALL_INVALID' && details?.field && ACCOUNT_FIELDS.has(details.field)) {
        setRefusedField(details.field)
      } else if (code === 'AGENT_INSTALL_LOGIN_NOT_FOUND') {
        // Deleted meanwhile: it leaves the list, and the dialog says why.
        onChange(logins.filter((l) => l.id !== editing.id))
        back()
        setError(loginErrorText(err, t))
      } else {
        setError(loginErrorText(err, t))
      }
    } finally {
      if (mounted.current) setBusy(false)
    }
  }

  const remove = async (l: AgentInstallLogin) => {
    const yes = await confirm({
      title: t('cluster.tasks.install.logins.deleteTitle', { name: l.name }),
      description: t('cluster.tasks.install.logins.deleteDescription'),
      confirmLabel: t('cluster.tasks.install.logins.delete'),
      danger: true,
    })
    if (!yes) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteAgentInstallLogin(l.id)
      onChange(logins.filter((x) => x.id !== l.id))
    } catch (err) {
      // One deleted meanwhile is gone all the same.
      if ((err as ApiError)?.code === 'AGENT_INSTALL_LOGIN_NOT_FOUND') onChange(logins.filter((x) => x.id !== l.id))
      else if (mounted.current) setError(loginErrorText(err, t))
    } finally {
      if (mounted.current) setBusy(false)
    }
  }

  let body
  if (editing && draft) {
    body = (
      <div data-testid="install-login-edit" className={cn(SCROLLS, '-mx-1 px-1 space-y-3')}>
        <div className="max-w-xs">
          <EditorField id={`${id}-name`} label={t('cluster.tasks.install.logins.name')} error={nameError}>
            <Input
              id={`${id}-name`}
              value={draft.name}
              onChange={(e) => {
                const name = e.target.value
                setDraft((d) => d && { ...d, name })
                setRefusedName(null)
              }}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={!!nameError || undefined}
              disabled={busy}
            />
          </EditorField>
        </div>
        <AccountFields
          id={id}
          value={draft}
          onChange={(patch) => {
            setDraft((d) => d && { ...d, ...patch })
            setRefusedField(null)
          }}
          problem={fieldProblem}
          stored={editing}
          disabled={busy}
        />
      </div>
    )
  } else if (logins.length === 0) {
    body = <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.install.logins.empty')}</p>
  } else {
    body = (
      // A name wraps between its words where it can.
      <ul data-testid="install-logins" className={cn(SCROLLS, 'rounded-xl border border-border divide-y divide-border')}>
        {logins.map((l) => (
          <li key={l.id} data-testid={`install-login-${l.id}`} className="flex flex-wrap items-start justify-between gap-x-3 gap-y-2 px-3 py-2 min-w-0">
            <div className="min-w-0 space-y-0.5">
              <p className="text-[13px] font-medium break-words">{l.name}</p>
              <p className="text-[12px] text-muted-foreground break-words">{l.user} · {methodText(l.method, t)}</p>
              <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.install.logins.changed', { when: formatDate(l.updated_at) })}</p>
            </div>
            <div className="flex shrink-0 gap-2">
              <Button variant="outline" size="sm" aria-label={t('cluster.tasks.install.logins.editLabel', { name: l.name })} onClick={() => edit(l)} disabled={busy}>
                <Pencil className="h-4 w-4" />
                {t('cluster.tasks.install.logins.edit')}
              </Button>
              <Button variant="outline" size="sm" aria-label={t('cluster.tasks.install.logins.deleteLabel', { name: l.name })} onClick={() => remove(l)} disabled={busy}>
                <Trash2 className="h-4 w-4" />
                {t('cluster.tasks.install.logins.delete')}
              </Button>
            </div>
          </li>
        ))}
      </ul>
    )
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="flex flex-col sm:max-w-xl">
        <DialogHeader className="shrink-0">
          <DialogTitle>{editing ? t('cluster.tasks.install.logins.editTitle') : t('cluster.tasks.install.logins.title')}</DialogTitle>
          <DialogDescription>
            {editing ? t('cluster.tasks.install.logins.editDescription') : t('cluster.tasks.install.logins.description')}
          </DialogDescription>
        </DialogHeader>
        {error && (
          <p role="alert" className="shrink-0 rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive break-words">{error}</p>
        )}
        {body}
        <DialogFooter className="shrink-0">
          {editing ? (
            <>
              <Button variant="outline" onClick={back} disabled={busy}>{t('common.cancel')}</Button>
              <Button onClick={saveChange} disabled={busy}>
                {busy && <Loader2 className="h-4 w-4 animate-spin" />}
                {t('common.save')}
              </Button>
            </>
          ) : (
            <Button variant="outline" onClick={onClose}>{t('common.close')}</Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
