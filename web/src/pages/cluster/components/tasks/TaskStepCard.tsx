import { useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { ArrowDown, ArrowUp, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import type { TaskPackagesStep, TaskServiceAction, TaskStep } from '@/types/api'
import {
  formatDuration,
  joinNames,
  packagesAction,
  parseDuration,
  splitNames,
  stepKindLabel,
  withFlag,
  withOptional,
  withPackageNames,
  withPackagesAction,
  DEFAULT_WAIT,
  type PackagesAction,
} from './taskForm'

interface TaskStepCardProps {
  step: TaskStep
  /** From 0. */
  index: number
  count: number
  onChange: (step: TaskStep) => void
  onMove: (by: -1 | 1) => void
  onRemove: () => void
  disabled?: boolean
}

const TEXTAREA = cn(
  'w-full min-h-24 rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[13px] shadow-xs outline-none',
  'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50 dark:bg-input/30',
)
const SELECT = 'min-h-9 w-full rounded-md border border-input bg-transparent px-3 text-[13px] dark:bg-input/30 disabled:opacity-50'

const SERVICE_ACTIONS: TaskServiceAction[] = ['start', 'stop', 'restart', 'enable', 'disable']
const PACKAGES_ACTIONS: PackagesAction[] = ['none', 'upgradeAll', 'upgrade', 'install', 'remove']
// One empty list: NamesField tells a list changed from outside by its identity.
const NO_NAMES: string[] = []

function serviceActionLabel(action: TaskServiceAction, t: TFunction): string {
  switch (action) {
    case 'start':
      return t('cluster.tasks.editor.service.start')
    case 'stop':
      return t('cluster.tasks.editor.service.stop')
    case 'restart':
      return t('cluster.tasks.editor.service.restart')
    case 'enable':
      return t('cluster.tasks.editor.service.enable')
    case 'disable':
      return t('cluster.tasks.editor.service.disable')
  }
}

function packagesActionLabel(action: PackagesAction, t: TFunction): string {
  switch (action) {
    case 'none':
      return t('cluster.tasks.editor.packages.none')
    case 'upgradeAll':
      return t('cluster.tasks.editor.packages.upgradeAll')
    case 'upgrade':
      return t('cluster.tasks.editor.packages.upgrade')
    case 'install':
      return t('cluster.tasks.editor.packages.install')
    case 'remove':
      return t('cluster.tasks.editor.packages.remove')
  }
}

export function EditorField({ id, label, hint, error, children }: { id: string; label: string; hint?: string; error?: string; children: ReactNode }) {
  return (
    <div className="min-w-0 space-y-1">
      <label htmlFor={id} className="text-[12px] font-medium text-muted-foreground">{label}</label>
      {children}
      {error ? <p className="text-[12px] text-destructive">{error}</p> : hint && <p className="text-[12px] text-muted-foreground">{hint}</p>}
    </div>
  )
}

export function EditorCheck({ id, label, checked, onChange, disabled }: { id: string; label: string; checked: boolean; onChange: (on: boolean) => void; disabled?: boolean }) {
  return (
    <div className="flex items-center gap-2 min-h-9">
      <Checkbox id={id} checked={checked} disabled={disabled} onCheckedChange={(on) => onChange(on === true)} />
      <label htmlFor={id} className="text-[13px] cursor-pointer">{label}</label>
    </div>
  )
}

// A duration typed as Go writes one (10m, 1h30m). The text is the field's
// own while it is typed; the step changes only when it reads as a duration,
// and a text that does not goes back to the step's value when the field is
// left. Empty is the server's default.
function DurationField({ id, label, value, onChange, disabled }: { id: string; label: string; value: number; onChange: (ns: number) => void; disabled?: boolean }) {
  const { t } = useTranslation()
  const shown = (ns: number) => (ns === 0 ? '' : formatDuration(ns))
  const [text, setText] = useState(() => shown(value))
  const [seen, setSeen] = useState(value)
  const read = (s: string) => (s.trim() === '' ? 0 : parseDuration(s))
  if (value !== seen) {
    setSeen(value)
    if (read(text) !== value) setText(shown(value))
  }
  const invalid = read(text) === null
  return (
    <EditorField
      id={id}
      label={label}
      hint={t('cluster.tasks.editor.durationDefault', { value: formatDuration(DEFAULT_WAIT) })}
      error={invalid ? t('cluster.tasks.editor.durationInvalid') : undefined}
    >
      <Input
        id={id}
        value={text}
        placeholder={formatDuration(DEFAULT_WAIT)}
        aria-invalid={invalid || undefined}
        disabled={disabled}
        className="w-32 font-mono"
        onChange={(e) => {
          setText(e.target.value)
          const ns = read(e.target.value)
          if (ns !== null) onChange(ns)
        }}
        onBlur={() => invalid && setText(shown(value))}
      />
    </EditorField>
  )
}

// Package names in one field. The text is kept as typed (a trailing space
// stays while the next name is typed); the step gets the names in it.
function NamesField({ id, label, names, onChange, disabled }: { id: string; label: string; names: string[]; onChange: (names: string[]) => void; disabled?: boolean }) {
  const { t } = useTranslation()
  const [text, setText] = useState(() => joinNames(names))
  const [seen, setSeen] = useState(names)
  if (names !== seen) {
    setSeen(names)
    if (joinNames(splitNames(text)) !== joinNames(names)) setText(joinNames(names))
  }
  return (
    <EditorField id={id} label={label} hint={t('cluster.tasks.editor.packages.namesHint')}>
      <Input
        id={id}
        value={text}
        disabled={disabled}
        className="font-mono"
        onChange={(e) => {
          setText(e.target.value)
          onChange(splitNames(e.target.value))
        }}
      />
    </EditorField>
  )
}

function PackagesFields({ id, p, onChange, disabled }: { id: string; p: TaskPackagesStep; onChange: (p: TaskPackagesStep) => void; disabled?: boolean }) {
  const { t } = useTranslation()
  const action = packagesAction(p)
  return (
    <div className="space-y-3">
      <EditorCheck
        id={`${id}-update`}
        label={t('cluster.tasks.editor.packages.update')}
        checked={!!p.update}
        disabled={disabled}
        onChange={(on) => onChange(withFlag(p, 'update', on))}
      />
      <EditorField id={`${id}-then`} label={t('cluster.tasks.editor.packages.then')}>
        <select
          id={`${id}-then`}
          value={action}
          disabled={disabled}
          className={SELECT}
          onChange={(e) => onChange(withPackagesAction(p, e.target.value as PackagesAction))}
        >
          {PACKAGES_ACTIONS.map((a) => (
            <option key={a} value={a}>{packagesActionLabel(a, t)}</option>
          ))}
        </select>
      </EditorField>
      {(action === 'upgrade' || action === 'install' || action === 'remove') && (
        <NamesField
          id={`${id}-names`}
          label={t('cluster.tasks.editor.packages.names')}
          names={p[action] ?? NO_NAMES}
          disabled={disabled}
          onChange={(names) => onChange(withPackageNames(p, names))}
        />
      )}
    </div>
  )
}

function KindFields({ id, step, onChange, disabled }: { id: string; step: TaskStep; onChange: (step: TaskStep) => void; disabled?: boolean }) {
  const { t } = useTranslation()
  switch (step.kind) {
    case 'packages':
      return <PackagesFields id={id} p={step.packages} disabled={disabled} onChange={(packages) => onChange({ ...step, packages })} />
    case 'service': {
      const s = step.service
      return (
        <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_12rem]">
          <EditorField id={`${id}-service`} label={t('cluster.tasks.editor.service.name')}>
            <Input
              id={`${id}-service`}
              value={s.name}
              placeholder="nginx.service"
              disabled={disabled}
              className="font-mono"
              onChange={(e) => onChange({ ...step, service: { ...s, name: e.target.value } })}
            />
          </EditorField>
          <EditorField id={`${id}-action`} label={t('cluster.tasks.editor.service.action')}>
            <select
              id={`${id}-action`}
              value={s.action}
              disabled={disabled}
              className={SELECT}
              onChange={(e) => onChange({ ...step, service: { ...s, action: e.target.value as TaskServiceAction } })}
            >
              {SERVICE_ACTIONS.map((a) => (
                <option key={a} value={a}>{serviceActionLabel(a, t)}</option>
              ))}
            </select>
          </EditorField>
        </div>
      )
    }
    case 'command': {
      const c = step.command
      const script = c.script !== undefined
      // Exactly one of run and script: switching carries the text across.
      const setScript = (on: boolean) => {
        if (on === script) return
        const text = (script ? c.script : c.run) ?? ''
        onChange({ ...step, command: on ? { script: text, timeout: c.timeout } : { run: text, timeout: c.timeout } })
      }
      return (
        <div className="space-y-3">
          <div role="radiogroup" className="flex flex-wrap gap-x-4 gap-y-1">
            {([false, true] as const).map((on) => (
              <label key={String(on)} className="flex items-center gap-2 min-h-9 text-[13px] cursor-pointer">
                <input
                  type="radio"
                  name={`${id}-mode`}
                  checked={script === on}
                  disabled={disabled}
                  onChange={() => setScript(on)}
                  className="accent-primary"
                />
                {on ? t('cluster.tasks.editor.command.script') : t('cluster.tasks.editor.command.oneLine')}
              </label>
            ))}
          </div>
          {script ? (
            <EditorField id={`${id}-script`} label={t('cluster.tasks.editor.command.script')}>
              <textarea
                id={`${id}-script`}
                value={c.script ?? ''}
                disabled={disabled}
                spellCheck={false}
                className={cn(TEXTAREA, 'min-h-32')}
                onChange={(e) => onChange({ ...step, command: { script: e.target.value, timeout: c.timeout } })}
              />
            </EditorField>
          ) : (
            <EditorField id={`${id}-run`} label={t('cluster.tasks.editor.command.run')}>
              <Input
                id={`${id}-run`}
                value={c.run ?? ''}
                disabled={disabled}
                spellCheck={false}
                className="font-mono"
                onChange={(e) => onChange({ ...step, command: { run: e.target.value, timeout: c.timeout } })}
              />
            </EditorField>
          )}
          <DurationField
            id={`${id}-timeout`}
            label={t('cluster.tasks.editor.command.timeout')}
            value={c.timeout}
            disabled={disabled}
            onChange={(timeout) => onChange({ ...step, command: { ...c, timeout } })}
          />
        </div>
      )
    }
    case 'file': {
      const f = step.file
      return (
        <div className="space-y-3">
          <EditorField id={`${id}-path`} label={t('cluster.tasks.editor.file.path')}>
            <Input
              id={`${id}-path`}
              value={f.path}
              placeholder="/etc/motd"
              disabled={disabled}
              className="font-mono"
              onChange={(e) => onChange({ ...step, file: { ...f, path: e.target.value } })}
            />
          </EditorField>
          <EditorField id={`${id}-content`} label={t('cluster.tasks.editor.file.content')}>
            <textarea
              id={`${id}-content`}
              value={f.content}
              disabled={disabled}
              spellCheck={false}
              className={TEXTAREA}
              onChange={(e) => onChange({ ...step, file: { ...f, content: e.target.value } })}
            />
          </EditorField>
          <div className="grid gap-3 grid-cols-2 sm:max-w-sm">
            <EditorField id={`${id}-mode`} label={t('cluster.tasks.editor.file.mode')}>
              <Input
                id={`${id}-mode`}
                value={f.mode ?? ''}
                placeholder="0644"
                disabled={disabled}
                className="font-mono"
                onChange={(e) => onChange({ ...step, file: withOptional(f, 'mode', e.target.value) })}
              />
            </EditorField>
            <EditorField id={`${id}-owner`} label={t('cluster.tasks.editor.file.owner')}>
              <Input
                id={`${id}-owner`}
                value={f.owner ?? ''}
                placeholder="root:root"
                disabled={disabled}
                className="font-mono"
                onChange={(e) => onChange({ ...step, file: withOptional(f, 'owner', e.target.value) })}
              />
            </EditorField>
          </div>
        </div>
      )
    }
    case 'stack': {
      const s = step.stack
      return (
        <div className="space-y-3">
          <EditorField id={`${id}-stack`} label={t('cluster.tasks.editor.stack.name')}>
            <Input
              id={`${id}-stack`}
              value={s.name}
              disabled={disabled}
              className="font-mono sm:max-w-xs"
              onChange={(e) => onChange({ ...step, stack: { ...s, name: e.target.value } })}
            />
          </EditorField>
          <EditorField id={`${id}-compose`} label={t('cluster.tasks.editor.stack.compose')}>
            <textarea
              id={`${id}-compose`}
              value={s.compose}
              disabled={disabled}
              spellCheck={false}
              className={cn(TEXTAREA, 'min-h-32')}
              onChange={(e) => onChange({ ...step, stack: { ...s, compose: e.target.value } })}
            />
          </EditorField>
          <EditorCheck
            id={`${id}-up`}
            label={t('cluster.tasks.editor.stack.up')}
            checked={!!s.up}
            disabled={disabled}
            onChange={(up) => onChange({ ...step, stack: { ...s, up } })}
          />
        </div>
      )
    }
    case 'reboot': {
      const r = step.reboot
      return (
        <div className="space-y-3">
          <EditorCheck
            id={`${id}-only`}
            label={t('cluster.tasks.editor.reboot.onlyIfRequired')}
            checked={r.only_if_required}
            disabled={disabled}
            onChange={(on) => onChange({ ...step, reboot: { ...r, only_if_required: on } })}
          />
          <DurationField
            id={`${id}-wait`}
            label={t('cluster.tasks.editor.reboot.wait')}
            value={r.wait}
            disabled={disabled}
            onChange={(wait) => onChange({ ...step, reboot: { ...r, wait } })}
          />
        </div>
      )
    }
  }
}

// One step of a task as a card: its kind's settings, then the name shown in
// a run and whether a failure lets the node carry on. The arrows move it,
// the bin removes it.
export function TaskStepCard({ step, index, count, onChange, onMove, onRemove, disabled }: TaskStepCardProps) {
  const { t } = useTranslation()
  const id = useId()
  const n = index + 1
  return (
    <section
      data-testid={`task-step-${index}`}
      aria-labelledby={`${id}-title`}
      className="min-w-0 rounded-xl border border-border bg-background/40 p-3 sm:p-4 space-y-3"
    >
      <div className="flex items-center gap-1">
        <h4 id={`${id}-title`} className="min-w-0 flex-1 text-[13px] font-semibold break-words">
          {t('cluster.tasks.plan.step', { n })} · {stepKindLabel(step.kind, t)}
        </h4>
        <Button type="button" variant="ghost" size="icon-sm" aria-label={t('cluster.tasks.editor.step.moveUp', { n })} disabled={disabled || index === 0} onClick={() => onMove(-1)}>
          <ArrowUp />
        </Button>
        <Button type="button" variant="ghost" size="icon-sm" aria-label={t('cluster.tasks.editor.step.moveDown', { n })} disabled={disabled || index === count - 1} onClick={() => onMove(1)}>
          <ArrowDown />
        </Button>
        <Button type="button" variant="ghost" size="icon-sm" aria-label={t('cluster.tasks.editor.step.remove', { n })} disabled={disabled} onClick={onRemove} className="text-destructive hover:text-destructive">
          <Trash2 />
        </Button>
      </div>
      <KindFields id={id} step={step} onChange={onChange} disabled={disabled} />
      <div className="grid gap-3 border-t border-border pt-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
        <EditorField id={`${id}-name`} label={t('cluster.tasks.editor.step.name')}>
          <Input
            id={`${id}-name`}
            value={step.name ?? ''}
            placeholder={stepKindLabel(step.kind, t)}
            disabled={disabled}
            onChange={(e) => onChange(withOptional(step, 'name', e.target.value))}
          />
        </EditorField>
        <EditorCheck
          id={`${id}-continue`}
          label={t('cluster.tasks.editor.step.continueOnError')}
          checked={!!step.continue_on_error}
          disabled={disabled}
          onChange={(on) => onChange(withFlag(step, 'continue_on_error', on))}
        />
      </div>
    </section>
  )
}
