import { useId, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Eye, EyeOff, Trash2 } from 'lucide-react'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { warningText } from '@/lib/warnings'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { StatusPill, type StatusPillTone } from '@/components/StatusPill'
import type { TaskImportFileState, TaskImportPreview, TaskImportResult } from '@/types/api'
import { repoLabel } from './taskList'
import { failedLine, importRequest, initialPicks, pickable, repoUrlValid, shortSha, type ImportFrom } from './taskImport'
import { useTaskDelete } from './useTaskDelete'

interface TaskGitImportDialogProps {
  /** [Pull again]: the repository, branch and folder its tasks came from. Nothing for a new import. */
  from?: ImportFrom
  onClose: () => void
  /** Reads the task list again: after an import, and after a task gone from the repository is deleted. */
  onChanged: () => Promise<void> | void
}

const STATE_TONE: Record<TaskImportFileState, StatusPillTone> = {
  new: 'primary',
  changed: 'warning',
  unchanged: 'muted',
  too_large: 'destructive',
}

// Importing tasks from a GitHub folder, in three steps: where to read (the
// repository, branch, folder, and a token for a private repository), what
// the folder holds at its commit now (new and changed files picked, the rest
// shown but not pickable, tasks whose file is gone offered for deletion), and
// what was imported and what was not. The token lives in this dialog's state
// only: sent in the two bodies, never stored, gone once the dialog closes.
// Mounted while open, so every opening starts afresh.
export function TaskGitImportDialog({ from, onClose, onChanged }: TaskGitImportDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const [url, setUrl] = useState(from?.url ?? '')
  const [branch, setBranch] = useState(from?.branch ?? 'main')
  const [path, setPath] = useState(from?.path ?? '')
  const [token, setToken] = useState('')
  const [tokenVisible, setTokenVisible] = useState(false)
  const [urlError, setUrlError] = useState(false)

  const [preview, setPreview] = useState<TaskImportPreview | null>(null)
  const [picked, setPicked] = useState<string[]>([])
  const [removed, setRemoved] = useState<TaskImportPreview['removed']>([])
  const [result, setResult] = useState<TaskImportResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const { remove, deleting } = useTaskDelete(onChanged, { stay: true })

  const stateText = (state: TaskImportFileState) => {
    switch (state) {
      case 'new': return t('cluster.tasks.import.state.new')
      case 'changed': return t('cluster.tasks.import.state.changed')
      case 'unchanged': return t('cluster.tasks.import.state.unchanged')
      case 'too_large': return t('cluster.tasks.import.state.tooLarge')
    }
  }

  async function onPreview(e: React.FormEvent) {
    e.preventDefault()
    if (!repoUrlValid(url)) {
      setUrlError(true)
      return
    }
    setUrlError(false)
    setError(null)
    setBusy(true)
    try {
      const answer = await api.previewTaskImport({ url: url.trim(), branch: branch.trim(), path: path.trim(), token: token || undefined })
      // Both lists are read from here on: an answer without one is an empty one.
      const p = { ...answer, files: answer.files ?? [], removed: answer.removed ?? [] }
      setPreview(p)
      setPicked(initialPicks(p))
      setRemoved(p.removed)
    } catch (err) {
      setError(taskErrorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  async function onImport() {
    if (!preview) return
    setError(null)
    setBusy(true)
    try {
      const r = await api.importTasks(importRequest(preview, picked, token))
      setResult(r)
      await onChanged()
    } catch (err) {
      setError(taskErrorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  async function onDeleteRemoved(taskId: string, name: string) {
    if (await remove(taskId, name)) setRemoved((list) => list.filter((x) => x.task_id !== taskId))
  }

  const errorBox = error && (
    <div role="alert" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive break-words">{error}</div>
  )

  let body
  let footer
  if (result) {
    const imported = result.imported ?? []
    const failed = result.failed ?? []
    body = (
      <div className="space-y-4">
        <section data-testid="import-result" className="space-y-1.5">
          <h3 className="text-[13px] font-semibold">{t('cluster.tasks.import.imported', { count: imported.length })}</h3>
          {imported.length > 0 && (
            <ul className="rounded-xl border border-border divide-y divide-border">
              {imported.map((task) => (
                <li key={task.id} className="px-3 py-2 text-[13px] space-y-0.5">
                  <Link to={`/cluster/tasks/${encodeURIComponent(task.id)}`} onClick={onClose} className="font-medium underline underline-offset-2 hover:text-primary break-words">
                    {task.name}
                  </Link>
                  {task.source.path && <p className="font-mono text-[12px] text-muted-foreground break-all">{task.source.path}</p>}
                </li>
              ))}
            </ul>
          )}
        </section>
        {failed.length > 0 && (
          <section data-testid="import-failed" className="space-y-1.5">
            <h3 className="text-[13px] font-semibold text-destructive">{t('cluster.tasks.import.failed', { count: failed.length })}</h3>
            <ul className="rounded-xl border border-destructive/30 divide-y divide-border">
              {failed.map((w, i) => {
                const where = failedLine(w)
                return (
                  <li key={`${i}-${where.path}`} className="px-3 py-2 text-[13px] space-y-0.5">
                    <p className="break-words">{warningText(w, t)}</p>
                    <p className="text-[12px] text-muted-foreground break-all">
                      <span className="font-mono">{where.path}</span>
                      {where.line !== null && <span> · {t('cluster.tasks.import.line', { line: where.line })}</span>}
                    </p>
                    {/* A document's problem is said only in the server's sentence. */}
                    {w.code === 'TASK_INVALID' && <p className="font-mono text-[12px] text-muted-foreground break-words">{w.message}</p>}
                  </li>
                )
              })}
            </ul>
          </section>
        )}
      </div>
    )
    footer = <Button onClick={onClose}>{t('common.close')}</Button>
  } else if (preview) {
    const files = preview.files
    body = (
      <div className="space-y-4">
        <p className="text-[13px] text-muted-foreground break-all">
          {t('cluster.tasks.import.readAt', {
            repo: repoLabel(preview.repo),
            branch: preview.branch,
            path: preview.path || '/',
            commit: shortSha(preview.commit),
          })}
        </p>
        {files.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.import.noFiles')}</p>
        ) : (
          <ul className="rounded-xl border border-border divide-y divide-border">
            {files.map((f) => {
              const box = `${id}-file-${f.path}`
              const can = pickable(f)
              return (
                <li key={f.path} className="flex items-start gap-2.5 px-3 py-2">
                  <Checkbox
                    id={box}
                    aria-label={f.path}
                    className="mt-0.5"
                    checked={can && picked.includes(f.path)}
                    disabled={!can || busy}
                    onCheckedChange={(on) => setPicked(on === true ? [...picked, f.path] : picked.filter((p) => p !== f.path))}
                  />
                  <label htmlFor={box} className="min-w-0 flex-1 space-y-0.5 cursor-pointer">
                    <span className="block text-[13px] font-medium break-all">{f.name}</span>
                    <span className="block font-mono text-[12px] text-muted-foreground break-all">{f.path}</span>
                    {f.task_name && <span className="block text-[12px] text-muted-foreground break-words">{t('cluster.tasks.import.asTask', { name: f.task_name })}</span>}
                  </label>
                  <StatusPill tone={STATE_TONE[f.state] ?? 'muted'} className="shrink-0">{stateText(f.state)}</StatusPill>
                </li>
              )
            })}
          </ul>
        )}
        {removed.length > 0 && (
          <section data-testid="import-removed" aria-labelledby={`${id}-removed`} className="space-y-1.5">
            <h3 id={`${id}-removed`} className="text-[13px] font-semibold">{t('cluster.tasks.import.removed')}</h3>
            <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.import.removedHint')}</p>
            <ul className="rounded-xl border border-border divide-y divide-border">
              {removed.map((r) => (
                <li key={r.task_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
                  <div className="min-w-0 flex-1">
                    <p className="text-[13px] font-medium break-words">{r.task_name}</p>
                    <p className="font-mono text-[12px] text-muted-foreground break-all">{r.path}</p>
                  </div>
                  <Button variant="outline" size="sm" className="text-destructive" disabled={deleting || busy} onClick={() => onDeleteRemoved(r.task_id, r.task_name)}>
                    <Trash2 className="h-4 w-4" />
                    {t('common.delete')}
                  </Button>
                </li>
              ))}
            </ul>
          </section>
        )}
        {errorBox}
      </div>
    )
    footer = (
      <>
        <Button variant="outline" disabled={busy} onClick={() => { setPreview(null); setError(null) }}>{t('common.back')}</Button>
        <Button loading={busy} disabled={picked.length === 0} onClick={onImport}>
          {t('cluster.tasks.import.importCount', { count: picked.length })}
        </Button>
      </>
    )
  } else {
    body = (
      <form id={`${id}-form`} onSubmit={onPreview} className="space-y-3">
        {from && <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.import.pullHint')}</p>}
        <div className="space-y-1">
          <Label htmlFor={`${id}-url`}>{t('cluster.tasks.import.url')}</Label>
          <Input
            id={`${id}-url`}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder={t('cluster.tasks.import.urlPlaceholder')}
            autoComplete="off"
            aria-invalid={urlError || undefined}
            disabled={busy}
          />
          {urlError && <p className="text-[12px] text-destructive">{t('cluster.tasks.import.urlInvalid')}</p>}
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div className="space-y-1">
            <Label htmlFor={`${id}-branch`}>{t('cluster.tasks.import.branch')}</Label>
            <Input id={`${id}-branch`} value={branch} onChange={(e) => setBranch(e.target.value)} autoComplete="off" disabled={busy} />
          </div>
          <div className="space-y-1 sm:col-span-2">
            <Label htmlFor={`${id}-path`}>{t('cluster.tasks.import.folder')}</Label>
            <Input id={`${id}-path`} value={path} onChange={(e) => setPath(e.target.value)} placeholder={t('cluster.tasks.import.folderPlaceholder')} autoComplete="off" disabled={busy} />
          </div>
        </div>
        <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.import.folderHint')}</p>
        <div className="space-y-1">
          <Label htmlFor={`${id}-token`}>{t('cluster.tasks.import.token')}</Label>
          <div className="relative">
            <Input
              id={`${id}-token`}
              type={tokenVisible ? 'text' : 'password'}
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="off"
              className="pr-10"
              disabled={busy}
            />
            <button
              type="button"
              onClick={() => setTokenVisible((v) => !v)}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              aria-label={tokenVisible ? t('cluster.tasks.import.tokenHide') : t('cluster.tasks.import.tokenShow')}
            >
              {tokenVisible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </button>
          </div>
          <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.import.tokenHint')}</p>
        </div>
        {errorBox}
      </form>
    )
    footer = (
      <>
        <Button variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
        <Button type="submit" form={`${id}-form`} loading={busy}>{t('cluster.tasks.import.preview')}</Button>
      </>
    )
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('cluster.tasks.import.title')}</DialogTitle>
          <DialogDescription>{t('cluster.tasks.import.description')}</DialogDescription>
        </DialogHeader>
        {body}
        <DialogFooter>{footer}</DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
