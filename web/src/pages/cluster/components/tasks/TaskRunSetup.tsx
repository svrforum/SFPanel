import { useTranslation } from 'react-i18next'
import type { TaskView } from '@/types/api'
import { builtinText } from './taskBuiltins'

interface TaskRunSetupProps {
  /** The task as the list has it; undefined when the list has no such ID. */
  task: TaskView | undefined
}

// The chosen task, set up to run: its description, the values it asks for,
// the targets, how it runs, the preview and the start. For now it names the
// task and says what it does.
export function TaskRunSetup({ task }: TaskRunSetupProps) {
  const { t } = useTranslation()
  if (!task) {
    return (
      <section data-testid="task-pane-setup" className="rounded-2xl bg-card p-5 card-shadow">
        <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.errors.notFound')}</p>
      </section>
    )
  }
  const description = builtinText(t, task.id, 'description', task.description ?? '')
  return (
    <section data-testid="task-pane-setup" className="rounded-2xl bg-card p-5 card-shadow space-y-2">
      <h2 className="text-[18px] font-bold break-words">{builtinText(t, task.id, 'name', task.name)}</h2>
      {description && <p className="text-[13px] text-muted-foreground whitespace-pre-wrap break-words">{description}</p>}
      <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.pane.notYet')}</p>
    </section>
  )
}
