import { useTranslation } from 'react-i18next'

interface TaskEditorProps {
  /** The task to change; absent for a new one. */
  taskId?: string
  /** Its name as the list shows it. */
  name?: string
}

// The task editor: step cards and YAML, two views of one document. It is
// loaded lazily (Monaco is large). For now it holds the place the routes
// tasks/new and tasks/:taskId/edit lead to.
export default function TaskEditor({ taskId, name }: TaskEditorProps) {
  const { t } = useTranslation()
  return (
    <section data-testid="task-pane-editor" className="rounded-2xl bg-card p-5 card-shadow space-y-2">
      <h2 className="text-[18px] font-bold break-words">{taskId ? (name ?? taskId) : t('cluster.tasks.list.new')}</h2>
      <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.pane.notYet')}</p>
    </section>
  )
}
