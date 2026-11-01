import { useTranslation } from 'react-i18next'

interface TaskRunViewProps {
  runId: string
}

// One run, under way or ended: the node-by-step table, a node's output, stop
// and rerun. For now it holds the place the route leads to.
export function TaskRunView({ runId }: TaskRunViewProps) {
  const { t } = useTranslation()
  return (
    <section data-testid="task-pane-run" className="rounded-2xl bg-card p-5 card-shadow space-y-2">
      <h2 className="text-[18px] font-bold">{t('cluster.tasks.pane.run')}</h2>
      <p className="text-[12px] text-muted-foreground font-mono break-all">{runId}</p>
      <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.pane.notYet')}</p>
    </section>
  )
}
