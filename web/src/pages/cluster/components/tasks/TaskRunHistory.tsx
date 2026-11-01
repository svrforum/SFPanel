import { useTranslation } from 'react-i18next'

// Every run this cluster keeps, newest first. For now it holds the place the
// route leads to.
export function TaskRunHistory() {
  const { t } = useTranslation()
  return (
    <section data-testid="task-pane-history" className="rounded-2xl bg-card p-5 card-shadow space-y-2">
      <h2 className="text-[18px] font-bold">{t('cluster.tasks.list.history')}</h2>
      <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.pane.notYet')}</p>
    </section>
  )
}
