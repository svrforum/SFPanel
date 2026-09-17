import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { AIProfile, AITool } from '@/types/api'
import { TOOL_META, aiErrorMessage, profileErrorMessage, relativeSince } from '@/lib/aiSessions'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useConfirm } from '@/components/ConfirmDialog'

export interface ProfilePanelProps {
  /**
   * The tool these profiles belong to. The caller renders the panel only for
   * a tool with profile support that is installed for `account` — a dropdown
   * offering a create field next to its own Install button would be creating
   * a login directory for a CLI that cannot use it.
   */
  tool: AITool
  /** The account whose logins these are; never '', which the caller gates on. */
  account: string
  /**
   * Re-read the page's session list. A session that ran on a deleted profile
   * still names it on its tab.
   */
  onSessionsChanged: () => void
}

/**
 * The 프로파일 block of one chip's dropdown: the logins this account keeps for
 * this tool, and a field to add one.
 *
 * It lives inside the DropdownMenuContent, which Radix mounts when the chip
 * opens and unmounts when it closes, so the list is fetched on the operator's
 * click and never on page load — three chips would otherwise have meant three
 * requests nobody asked for. The same unmount is why a delete reports through
 * a toast: opening the confirmation dialog is an interaction outside the menu,
 * which closes it, and by the time the answer arrives this component is gone.
 * Refreshing the session list is therefore the page's job, not this one's.
 */
export function ProfilePanel({ tool, account, onSessionsChanged }: ProfilePanelProps) {
  const { t, i18n } = useTranslation()
  const confirm = useConfirm()
  const [profiles, setProfiles] = useState<AIProfile[] | null>(null)
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  // Two in-flight flags, not one. `creating` belongs to the name field below
  // and `deleting` to one row: the create flag used to disable every row's
  // trash button, which it has nothing to do with, while a delete disabled
  // neither its own row nor anything else.
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<string | null>(null)

  const load = useCallback(() => {
    let cancelled = false
    api.getAIProfiles(account, tool)
      .then((r) => { if (!cancelled) { setProfiles(r.profiles); setError(null) } })
      // A GET, so no profile surface maps its codes: INVALID_BODY here would
      // mean a request the panel itself built wrongly.
      .catch((err: unknown) => { if (!cancelled) setError(aiErrorMessage(err, t)) })
    return () => { cancelled = true }
  }, [account, tool, t])
  useEffect(() => load(), [load])

  const create = async () => {
    const value = name.trim()
    if (!value) return
    setCreating(true)
    setError(null)
    try {
      // The server answers with the directory it made; the list is re-read
      // rather than appended to, so a row the panel invented can never
      // disagree with what is on disk.
      await api.createAIProfile({ user: account, tool, name: value })
      setName('')
      load()
    } catch (err: unknown) {
      setError(profileErrorMessage(err, t, 'create'))
    } finally {
      setCreating(false)
    }
  }

  const remove = async (p: AIProfile) => {
    // The whole confirm-then-DELETE window is one delete, so the re-entry
    // guard and the row's disabled state cover the confirmation too, not just
    // the request.
    if (deleting !== null) return
    setDeleting(p.name)
    try {
      const ok = await confirm({
        title: t('ai.profiles.deleteConfirmTitle', { name: p.name }),
        description: t('ai.profiles.deleteConfirmDesc', { account, tool: TOOL_META[tool].label }),
        confirmLabel: t('ai.profiles.delete'),
        danger: true,
      })
      if (!ok) return
      await api.deleteAIProfile(account, tool, p.name)
      toast.success(t('ai.profiles.deleted', { name: p.name }))
      load()
      // A session that ran on the profile still names it on its tab.
      onSessionsChanged()
    } catch (err: unknown) {
      // AI_PROFILE_IN_USE lands here: the panel refuses to delete a profile a
      // live session is on, and the toast is what the operator still sees
      // after the confirmation closed this menu.
      toast.error(profileErrorMessage(err, t, 'delete'))
    } finally {
      setDeleting(null)
    }
  }

  return (
    <div className="space-y-1.5">
      <p className="text-[12px] text-muted-foreground">{t('ai.profiles.manage')}</p>
      {profiles === null ? (
        error === null && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" aria-label={t('ai.tools.checking')} />
      ) : (
        <ul className="space-y-1">
          {profiles.map((p) => {
            const used = relativeSince(p.last_used_at || '', i18n.language)
            const busy = deleting === p.name
            return (
              <li key={p.path} className="flex items-center gap-1.5 text-[12px]">
                <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', p.logged_in ? 'bg-success' : 'bg-muted-foreground/40')} aria-hidden="true" />
                <span className="sr-only">{p.logged_in ? t('ai.profiles.loggedIn') : t('ai.profiles.notLoggedIn')}</span>
                <span className={cn('truncate min-w-0', !p.default && 'font-mono')}>{p.default ? t('ai.profiles.default') : p.name}</span>
                <span className="ml-auto flex items-center gap-1 shrink-0">
                  {used && <span className="text-[11px] text-muted-foreground">{t('ai.profiles.lastUsed', { when: used })}</span>}
                  {/* The default profile is the tool's own directory, which is
                      not the panel's to remove — the route refuses it too. */}
                  {!p.default && (
                    <Button variant="ghost" size="sm" className="h-6 w-6 p-0 text-muted-foreground hover:text-destructive"
                      aria-label={t('ai.profiles.delete')} title={t('ai.profiles.delete')}
                      disabled={busy} onClick={() => void remove(p)}>
                      {busy
                        ? <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
                        : <Trash2 className="h-3.5 w-3.5" />}
                    </Button>
                  )}
                </span>
              </li>
            )
          })}
        </ul>
      )}
      <div className="flex items-center gap-1.5">
        <Input value={name} onChange={(e) => setName(e.target.value)}
          // A menu treats bare characters as typeahead and moves focus to the
          // item they match, so the keystrokes have to stop at the input.
          // Escape is the exception: it still belongs to the menu, which is
          // how the operator closes the chip from the keyboard.
          onKeyDown={(e) => {
            if (e.key === 'Escape') return
            e.stopPropagation()
            // Guarded like the dialog's: a second Enter in flight posts the
            // same name and paints its 409 over a create that succeeded.
            if (e.key === 'Enter') { e.preventDefault(); if (!creating) void create() }
          }}
          placeholder={t('ai.profiles.namePlaceholder')} className="h-7 font-mono text-[12px]" maxLength={32} spellCheck={false}
          aria-label={t('ai.profiles.label')} />
        <Button variant="outline" size="sm" className="h-7 rounded-lg shrink-0" disabled={creating || !name.trim()} onClick={() => void create()}>
          {creating ? <Loader2 className="animate-spin" aria-hidden="true" /> : null}
          {creating ? t('ai.profiles.creating') : t('common.create')}
        </Button>
      </div>
      {error && <p role="alert" className="text-[11px] text-destructive">{error}</p>}
    </div>
  )
}
