import { lazy, Suspense, useEffect, useId, useState, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { AlertTriangle, Copy, Loader2, Lock, Plus, Save, Trash2 } from 'lucide-react'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useConfirm } from '@/components/ConfirmDialog'
import type { ClusterNode, TaskInput, TaskModel, TaskProblem, TaskStepKind, TaskView } from '@/types/api'
import { builtinText } from './taskBuiltins'
import { TaskTargetPicker } from './TaskTargetPicker'
import { EditorCheck, EditorField, TaskStepCard } from './TaskStepCard'
import {
  blankStep,
  blankTask,
  clonePath,
  cloneModel,
  copyName,
  documentTooLarge,
  hasYamlComments,
  inputNameValid,
  modelTargets,
  moveItem,
  pickedTargets,
  stepKindLabel,
  withFlag,
  withOptional,
  MAX_DOCUMENT_BYTES,
  MAX_STEPS,
  STEP_KINDS,
} from './taskForm'
import { draftFromModel, draftFromTask, draftKey, dropDraft, keepDraft, readDraft, type TaskDraft } from './taskDrafts'
import { listShows, type ListHolds } from './taskListModel'
import { useMounted } from './useMounted'
import { useTaskDelete } from './useTaskDelete'

// Monaco is only fetched when the YAML view is opened.
const TaskYamlEditor = lazy(() => import('./TaskYamlEditor'))

interface TaskEditorProps {
  /** The task to change; absent for a new one. */
  taskId?: string
  /** Its name as the list shows it. */
  name?: string
  /** Reads the list again after a save or a delete, until it shows what was written; the page moves once it has. */
  onSaved: (holds?: ListHolds) => Promise<void> | void
}

const NO_PROBLEMS: TaskProblem[] = []
const byName = (a: ClusterNode, b: ClusterNode) => a.name.localeCompare(b.name, undefined, { numeric: true })
const taskPath = (id: string) => `/cluster/tasks/${encodeURIComponent(id)}`

// The task editor: one document, two views of it. The form edits the model
// validate returns as step cards; the YAML view is the document in Monaco.
// The web has no YAML parser, so the server crosses between them: the form
// becomes YAML through format, YAML becomes the form through validate (and
// only when it is valid). A save checks the document with validate first,
// whose 200 carries the problems a refused save would lose, and only a
// valid one is sent. tasks/new?from=<id> opens a clone of that task.
export default function TaskEditor({ taskId, name, onSaved }: TaskEditorProps) {
  const [params] = useSearchParams()
  const from = taskId ? null : params.get('from')
  const key = draftKey(taskId, from)
  return <EditorLoader key={key} draft={key} taskId={taskId} from={from} name={name} onSaved={onSaved} />
}

function Pane({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <section data-testid="task-pane-editor" className={cn('min-w-0 rounded-2xl bg-card p-4 sm:p-5 card-shadow space-y-5', className)}>
      {children}
    </section>
  )
}

interface EditorLoaderProps {
  draft: string
  taskId?: string
  from: string | null
  name?: string
  onSaved: (holds?: ListHolds) => Promise<void> | void
}

// What the editor opens with: a draft kept from before, a new task, a clone
// of a task, or a stored task. A read-only task (built in, or from Git) is
// shown as it is, with [Clone and edit] in place of a form that cannot save.
function EditorLoader({ draft, taskId, from, name, onSaved }: EditorLoaderProps) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [kept, setKept] = useState(() => readDraft(draft))
  const [attempt, setAttempt] = useState(0)
  const [fetched, setFetched] = useState<{ view: TaskView } | { error: unknown } | null>(null)
  const source = taskId ?? from

  useEffect(() => {
    if (source === null || kept) return
    let live = true
    api.getTask(source).then(
      (view) => live && setFetched({ view }),
      (error) => live && setFetched({ error }),
    )
    return () => { live = false }
  }, [source, kept, attempt])

  const discard = async () => {
    const yes = await confirm({
      title: t('cluster.tasks.editor.discardTitle'),
      description: t('cluster.tasks.editor.discardDescription'),
      confirmLabel: t('cluster.tasks.editor.discardConfirm'),
      danger: true,
    })
    if (!yes) return
    dropDraft(draft)
    setKept(undefined)
    setFetched(null)
    setAttempt((n) => n + 1)
  }

  const back = taskId ? taskPath(taskId) : from ? taskPath(from) : '/cluster/tasks'
  const editor = (init: () => TaskDraft, heading: string) => (
    <DocumentEditor
      key={attempt}
      draftKey={draft}
      taskId={taskId}
      heading={heading}
      back={back}
      init={init}
      restored={!!kept}
      onDiscard={discard}
      onSaved={onSaved}
    />
  )

  const newTitle = t('cluster.tasks.editor.title')
  if (kept) return editor(() => kept, taskId ? (name ?? taskId) : newTitle)
  if (source === null) return editor(() => draftFromModel(blankTask()), newTitle)
  if (!fetched) {
    return (
      <Pane>
        <h2 className="text-[18px] font-bold break-words">{taskId ? (name ?? taskId) : newTitle}</h2>
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </Pane>
    )
  }
  if ('error' in fetched) {
    return (
      <Pane>
        <p role="alert" className="text-[13px] text-destructive">{t('cluster.tasks.setup.loadError')}: {taskErrorMessage(fetched.error, t)}</p>
      </Pane>
    )
  }
  const { view } = fetched
  const text = (field: string, fallback: string) => builtinText(t, view.id, field, fallback)
  if (taskId) {
    if (view.read_only) return <ReadOnlyTask view={view} heading={text('name', view.name)} onDeleted={onSaved} />
    return editor(() => draftFromTask(view), view.name)
  }
  // A clone: the source's model, renamed and, for a built-in, in the
  // screen's words. A source that no longer parses opens as its YAML.
  return editor(
    () => (view.task ? draftFromModel(cloneModel(view.task, (n) => copyName(n, t), text)) : draftFromTask(view)),
    newTitle,
  )
}

function ReadOnlyTask({ view, heading, onDeleted }: { view: TaskView; heading: string; onDeleted: (holds?: ListHolds) => Promise<void> | void }) {
  const { t } = useTranslation()
  const { remove, deleting } = useTaskDelete(onDeleted)
  const git = view.source.kind === 'git'
  return (
    <Pane>
      <h2 className="text-[18px] font-bold break-words">{heading}</h2>
      <p className="flex items-start gap-2 rounded-xl bg-secondary/60 px-3 py-2 text-[13px]">
        <Lock className="h-4 w-4 mt-0.5 shrink-0" aria-hidden />
        {git ? t('cluster.tasks.editor.readOnlyGit') : t('cluster.tasks.editor.readOnlyBuiltin')}
      </p>
      <div className="flex flex-wrap gap-2">
        <Button asChild>
          <Link to={clonePath(view.id)}>
            <Copy className="h-4 w-4" />
            {t('cluster.tasks.editor.clone')}
          </Link>
        </Button>
        {git && (
          <Button variant="outline" className="text-destructive" loading={deleting} onClick={() => remove(view.id, heading)}>
            <Trash2 className="h-4 w-4" />
            {t('common.delete')}
          </Button>
        )}
      </div>
      {view.yaml && (
        <pre className="max-h-96 overflow-auto rounded-xl bg-zinc-950 p-4 font-mono text-[12px] text-zinc-100 whitespace-pre">{view.yaml}</pre>
      )}
    </Pane>
  )
}

interface DocumentEditorProps {
  draftKey: string
  taskId?: string
  heading: string
  /** Where Cancel goes. */
  back: string
  init: () => TaskDraft
  /** Opened from a draft kept from before. */
  restored: boolean
  onDiscard: () => void
  onSaved: (holds?: ListHolds) => Promise<void> | void
}

interface Problems {
  list: TaskProblem[]
  /** The document they were found in. */
  yaml: string
}

function DocumentEditor({ draftKey: key, taskId, heading, back, init, restored, onDiscard, onSaved }: DocumentEditorProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const mounted = useMounted()
  const confirm = useConfirm()
  const id = useId()
  const [doc, setDoc] = useState<TaskDraft>(init)
  const [dirty, setDirty] = useState(restored)
  const [problems, setProblems] = useState<Problems | null>(null)
  const [jump, setJump] = useState<{ line: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notValid, setNotValid] = useState(false)
  const [nodes, setNodes] = useState<ClusterNode[] | null>(null)
  const [nodesError, setNodesError] = useState<unknown>(null)
  const { remove, deleting } = useTaskDelete(onSaved)

  useEffect(() => {
    let live = true
    api.getClusterNodes().then(
      (r) => live && setNodes([...r.nodes].sort(byName)),
      (err) => {
        if (!live) return
        setNodes([])
        setNodesError(err)
      },
    )
    return () => { live = false }
  }, [])

  // A change outlives a trip to another menu; a reload or a closed tab asks.
  useEffect(() => {
    if (dirty) keepDraft(key, doc)
    else dropDraft(key)
  }, [dirty, doc, key])
  useEffect(() => {
    if (!dirty) return
    const ask = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', ask)
    return () => window.removeEventListener('beforeunload', ask)
  }, [dirty])

  const edit = (fn: (d: TaskDraft) => TaskDraft) => {
    setDoc(fn)
    setDirty(true)
    setNotValid(false)
  }

  // The YAML the model is: kept from the last format (or the stored
  // document) while the model is the same, asked of the server otherwise.
  const yamlOf = async (d: TaskDraft) =>
    d.formatted && d.formatted.model === d.model ? d.formatted.yaml : (await api.formatTask(d.model)).yaml

  const toYaml = async (line?: number) => {
    setBusy(true)
    setError(null)
    try {
      const yaml = await yamlOf(doc)
      let next = problems
      // Problems found in another document are checked again in this one.
      if (problems && problems.yaml !== yaml) {
        const v = await api.validateTask(yaml)
        next = v.valid ? null : { list: v.problems, yaml }
      }
      setDoc((d) => ({ ...d, view: 'yaml', yaml, formatted: { yaml, model: d.model } }))
      setProblems(next)
      // A line asked for before is not asked for again when the view comes back.
      setJump(line !== undefined ? { line } : null)
    } catch (err) {
      setError(taskErrorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  const toForm = async () => {
    if (doc.formatted && doc.formatted.yaml === doc.yaml && doc.formatted.model === doc.model) {
      setDoc((d) => ({ ...d, view: 'form' }))
      return
    }
    // The form writes the document anew: what is not in the model is lost.
    if (hasYamlComments(doc.yaml)) {
      const yes = await confirm({
        title: t('cluster.tasks.editor.commentsTitle'),
        description: t('cluster.tasks.editor.commentsDescription'),
        confirmLabel: t('cluster.tasks.editor.commentsConfirm'),
      })
      if (!yes) return
    }
    const yaml = doc.yaml
    setBusy(true)
    setError(null)
    setNotValid(false)
    try {
      const v = await api.validateTask(yaml)
      if (!v.valid || !v.task) {
        setProblems({ list: v.problems, yaml })
        setNotValid(true)
        return
      }
      const model = v.task
      setDoc((d) => ({ ...d, view: 'form', model, keys: model.steps.map((_, i) => i), formatted: { yaml, model } }))
      setProblems(null)
    } catch (err) {
      setError(taskErrorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  const save = async () => {
    setBusy(true)
    setError(null)
    setNotValid(false)
    try {
      const fromForm = doc.view === 'form'
      const yaml = fromForm ? await yamlOf(doc) : doc.yaml
      if (fromForm) {
        const model = doc.model
        setDoc((d) => (d.model === model ? { ...d, formatted: { yaml, model } } : d))
      }
      if (documentTooLarge(yaml)) {
        setError(t('cluster.tasks.errors.taskTooLarge', { max_kib: MAX_DOCUMENT_BYTES >> 10 }))
        return
      }
      const v = await api.validateTask(yaml)
      if (!v.valid) {
        setProblems({ list: v.problems, yaml })
        return
      }
      setProblems(null)
      const saved = taskId ? await api.updateTask(taskId, yaml) : await api.createTask(yaml)
      dropDraft(key)
      setDirty(false)
      toast.success(t('cluster.tasks.editor.saved'))
      await onSaved(listShows([saved]))
      // Saved and listed either way; the page moves only if the operator is still here.
      if (mounted.current) navigate(taskPath(saved.id))
    } catch (err) {
      setError(taskErrorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  const cancel = async () => {
    if (dirty) {
      const yes = await confirm({
        title: t('cluster.tasks.editor.discardTitle'),
        description: t('cluster.tasks.editor.discardDescription'),
        confirmLabel: t('cluster.tasks.editor.discardConfirm'),
        danger: true,
      })
      if (!yes) return
    }
    dropDraft(key)
    setDirty(false)
    navigate(back)
  }

  const showProblem = (line: number) => {
    if (doc.view === 'yaml') setJump({ line })
    else void toYaml(line)
  }

  const lock = busy || deleting
  return (
    <Pane>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="min-w-0 text-[18px] font-bold break-words">{heading}</h2>
        <div role="group" aria-label={t('cluster.tasks.editor.view')} className="inline-flex rounded-lg bg-secondary p-0.5">
          {(['form', 'yaml'] as const).map((v) => (
            <button
              key={v}
              type="button"
              aria-pressed={doc.view === v}
              disabled={lock}
              onClick={() => {
                if (doc.view === v) return
                if (v === 'yaml') void toYaml()
                else void toForm()
              }}
              className={cn(
                'min-h-8 rounded-md px-3 text-[13px] font-medium transition-colors disabled:opacity-50',
                doc.view === v ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
              )}
            >
              {v === 'form' ? t('cluster.tasks.editor.viewForm') : t('cluster.tasks.editor.viewYaml')}
            </button>
          ))}
        </div>
      </div>

      {restored && (
        <div role="status" className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl bg-primary/10 px-3 py-2 text-[13px]">
          <span>{t('cluster.tasks.editor.restored')}</span>
          <Button type="button" variant="link" size="sm" className="h-auto px-0" disabled={lock} onClick={onDiscard}>
            {t('cluster.tasks.editor.discard')}
          </Button>
        </div>
      )}

      {doc.view === 'form' ? (
        <FormView id={id} doc={doc} edit={edit} nodes={nodes} nodesError={nodesError} disabled={lock} />
      ) : (
        <Suspense fallback={<Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />}>
          <TaskYamlEditor
            value={doc.yaml}
            onChange={(yaml) => yaml !== doc.yaml && edit((d) => ({ ...d, yaml }))}
            problems={problems?.list ?? NO_PROBLEMS}
            jump={jump}
            label={t('cluster.tasks.editor.yamlLabel')}
          />
        </Suspense>
      )}

      {notValid && (
        <p role="alert" className="flex items-start gap-2 text-[13px] text-destructive">
          <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" aria-hidden />
          {t('cluster.tasks.editor.toFormInvalid')}
        </p>
      )}
      {problems && problems.list.length > 0 && (
        <section aria-labelledby={`${id}-problems`} className="space-y-2 rounded-xl bg-destructive/10 p-3">
          <h3 id={`${id}-problems`} className="text-[13px] font-semibold text-destructive">{t('cluster.tasks.editor.problems')}</h3>
          <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.editor.problemsHint')}</p>
          <ul className="space-y-1">
            {problems.list.map((p, i) => (
              <li key={i}>
                <button
                  type="button"
                  disabled={lock}
                  onClick={() => showProblem(Math.max(p.line, 1))}
                  className="w-full rounded-lg px-2 py-1.5 text-left text-[13px] hover:bg-destructive/10 break-words"
                >
                  <span className="font-medium">
                    {p.line > 0 ? t('cluster.tasks.editor.problemLine', { line: p.line }) : t('cluster.tasks.editor.problemDocument')}
                  </span>
                  {': '}
                  <span className="font-mono text-[12px]">{p.message}</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {error && <p role="alert" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive break-words">{error}</p>}

      <div className="flex flex-wrap gap-2">
        <Button type="button" onClick={save} disabled={lock}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
          {t('common.save')}
        </Button>
        <Button type="button" variant="outline" onClick={cancel} disabled={lock}>
          {t('common.cancel')}
        </Button>
        {taskId && (
          <Button type="button" variant="outline" className="sm:ml-auto text-destructive" loading={deleting} disabled={busy} onClick={() => remove(taskId, heading)}>
            <Trash2 className="h-4 w-4" />
            {t('common.delete')}
          </Button>
        )}
      </div>
    </Pane>
  )
}

interface FormViewProps {
  id: string
  doc: TaskDraft
  edit: (fn: (d: TaskDraft) => TaskDraft) => void
  nodes: ClusterNode[] | null
  nodesError: unknown
  disabled: boolean
}

// The document as a form: name and description, the inputs a run asks for,
// the targets a run starts from, how it runs, and the steps as cards.
function FormView({ id, doc, edit, nodes, nodesError, disabled }: FormViewProps) {
  const { t } = useTranslation()
  const m = doc.model
  const inputs = m.inputs ?? []
  const setModel = (fn: (m: TaskModel) => TaskModel) => edit((d) => ({ ...d, model: fn(d.model) }))
  const setInputs = (fn: (list: TaskInput[]) => TaskInput[]) =>
    setModel((mm) => {
      const list = fn(mm.inputs ?? [])
      const next: TaskModel = { ...mm, inputs: list }
      if (list.length === 0) delete next.inputs
      return next
    })
  const setInput = (i: number, fn: (input: TaskInput) => TaskInput) => setInputs((list) => list.map((x, j) => (j === i ? fn(x) : x)))

  const addStep = (kind: TaskStepKind) =>
    edit((d) => ({
      ...d,
      model: { ...d.model, steps: [...d.model.steps, blankStep(kind)] },
      keys: [...d.keys, d.keys.length === 0 ? 0 : Math.max(...d.keys) + 1],
    }))
  const moveStep = (i: number, by: -1 | 1) =>
    edit((d) => ({ ...d, model: { ...d.model, steps: moveItem(d.model.steps, i, by) }, keys: moveItem(d.keys, i, by) }))
  const removeStep = (i: number) =>
    edit((d) => ({
      ...d,
      model: { ...d.model, steps: d.model.steps.filter((_, j) => j !== i) },
      keys: d.keys.filter((_, j) => j !== i),
    }))

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <EditorField id={`${id}-name`} label={t('cluster.tasks.editor.name')}>
          <Input id={`${id}-name`} value={m.name} disabled={disabled} onChange={(e) => setModel((mm) => ({ ...mm, name: e.target.value }))} />
        </EditorField>
        <EditorField id={`${id}-description`} label={t('cluster.tasks.editor.description')}>
          <textarea
            id={`${id}-description`}
            value={m.description ?? ''}
            disabled={disabled}
            rows={2}
            className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-[13px] shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50 dark:bg-input/30"
            onChange={(e) => setModel((mm) => withOptional(mm, 'description', e.target.value))}
          />
        </EditorField>
      </div>

      <section aria-labelledby={`${id}-inputs`} className="space-y-3">
        <div className="space-y-1">
          <h3 id={`${id}-inputs`} className="text-[14px] font-semibold">{t('cluster.tasks.editor.inputs')}</h3>
          <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.editor.inputsHint', { example: '{{ name }}' })}</p>
        </div>
        {inputs.map((input, i) => {
          const field = `${id}-input-${i}`
          const nameBad = input.name !== '' && !inputNameValid(input.name)
          return (
            <div
              key={i}
              data-testid={`task-input-${i}`}
              className="grid min-w-0 gap-3 rounded-xl border border-border p-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)_minmax(0,1fr)]"
            >
              <EditorField id={`${field}-name`} label={t('cluster.tasks.editor.inputName')} error={nameBad ? t('cluster.tasks.editor.inputNameInvalid') : undefined}>
                <Input
                  id={`${field}-name`}
                  value={input.name}
                  disabled={disabled}
                  aria-invalid={nameBad || undefined}
                  className="font-mono"
                  onChange={(e) => setInput(i, (x) => ({ ...x, name: e.target.value }))}
                />
              </EditorField>
              <EditorField id={`${field}-label`} label={t('cluster.tasks.editor.inputLabel')}>
                <Input id={`${field}-label`} value={input.label ?? ''} disabled={disabled} onChange={(e) => setInput(i, (x) => withOptional(x, 'label', e.target.value))} />
              </EditorField>
              <EditorField id={`${field}-default`} label={t('cluster.tasks.editor.inputDefault')}>
                <Input id={`${field}-default`} value={input.default ?? ''} disabled={disabled} onChange={(e) => setInput(i, (x) => withOptional(x, 'default', e.target.value))} />
              </EditorField>
              <div className="flex flex-wrap items-center gap-x-4 sm:col-span-2 lg:col-span-3">
                <EditorCheck id={`${field}-required`} label={t('cluster.tasks.editor.inputRequired')} checked={!!input.required} disabled={disabled} onChange={(on) => setInput(i, (x) => withFlag(x, 'required', on))} />
                <EditorCheck id={`${field}-raw`} label={t('cluster.tasks.editor.inputRaw')} checked={!!input.raw} disabled={disabled} onChange={(on) => setInput(i, (x) => withFlag(x, 'raw', on))} />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="ml-auto text-destructive hover:text-destructive"
                  aria-label={t('cluster.tasks.editor.removeInput', { n: i + 1 })}
                  disabled={disabled}
                  onClick={() => setInputs((list) => list.filter((_, j) => j !== i))}
                >
                  <Trash2 />
                </Button>
              </div>
            </div>
          )
        })}
        <Button type="button" variant="outline" size="sm" disabled={disabled} onClick={() => setInputs((list) => [...list, { name: '' }])}>
          <Plus className="h-4 w-4" />
          {t('cluster.tasks.editor.addInput')}
        </Button>
      </section>

      <div className="space-y-2">
        <TaskTargetPicker
          nodes={nodes}
          nodesError={nodesError ? taskErrorMessage(nodesError, t) : null}
          targets={pickedTargets(m.targets)}
          onChange={(picked) => setModel((mm) => ({ ...mm, targets: modelTargets(picked) }))}
          disabled={disabled}
        />
        <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.editor.targetsHint')}</p>
      </div>

      <section aria-labelledby={`${id}-how`} className="space-y-3">
        <h3 id={`${id}-how`} className="text-[14px] font-semibold">{t('cluster.tasks.setup.howItRuns')}</h3>
        <div className="flex flex-wrap gap-x-6 gap-y-3">
          <div className="space-y-1">
            <label htmlFor={`${id}-batch`} className="text-[13px] font-medium">{t('cluster.tasks.setup.batch')}</label>
            <Input
              id={`${id}-batch`}
              type="number"
              min={1}
              step={1}
              inputMode="numeric"
              value={m.batch === 0 ? '' : m.batch}
              disabled={disabled}
              onChange={(e) => {
                const n = Math.trunc(Number(e.target.value))
                setModel((mm) => ({ ...mm, batch: e.target.value === '' || !Number.isFinite(n) ? 0 : n }))
              }}
              className="w-28"
            />
            <p className="text-[12px] text-muted-foreground max-w-xs">{t('cluster.tasks.setup.batchHint')}</p>
          </div>
          <div className="space-y-1">
            <EditorCheck
              id={`${id}-stop`}
              label={t('cluster.tasks.setup.stopOnFailure')}
              checked={m.stop_on_failure}
              disabled={disabled}
              onChange={(on) => setModel((mm) => ({ ...mm, stop_on_failure: on }))}
            />
            <p className="text-[12px] text-muted-foreground max-w-xs">{t('cluster.tasks.setup.stopOnFailureHint')}</p>
          </div>
        </div>
      </section>

      <section aria-labelledby={`${id}-steps`} className="space-y-3">
        <div className="space-y-1">
          <h3 id={`${id}-steps`} className="text-[14px] font-semibold">{t('cluster.tasks.editor.steps')}</h3>
          <p className="text-[12px] text-muted-foreground break-words">
            {t('cluster.tasks.editor.stepsHint', { input: '{{ name }}', nodeName: '{{ node.name }}', nodeAddress: '{{ node.address }}' })}
          </p>
        </div>
        {m.steps.length === 0 && <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.editor.noSteps')}</p>}
        {m.steps.map((step, i) => (
          <TaskStepCard
            key={doc.keys[i] ?? `extra-${i}`}
            step={step}
            index={i}
            count={m.steps.length}
            disabled={disabled}
            onChange={(next) => setModel((mm) => ({ ...mm, steps: mm.steps.map((s, j) => (j === i ? next : s)) }))}
            onMove={(by) => moveStep(i, by)}
            onRemove={() => removeStep(i)}
          />
        ))}
        <div className="flex flex-wrap items-center gap-3">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button type="button" variant="outline" size="sm" disabled={disabled || m.steps.length >= MAX_STEPS}>
                <Plus className="h-4 w-4" />
                {t('cluster.tasks.editor.addStep')}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              {STEP_KINDS.map((kind) => (
                <DropdownMenuItem key={kind} onSelect={() => addStep(kind)}>
                  {stepKindLabel(kind, t)}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          {m.steps.length >= MAX_STEPS && <span className="text-[12px] text-muted-foreground">{t('cluster.tasks.editor.stepsFull', { max: MAX_STEPS })}</span>}
        </div>
      </section>
    </div>
  )
}
