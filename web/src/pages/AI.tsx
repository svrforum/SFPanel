import { useTranslation } from 'react-i18next'

export default function AI() {
  const { t } = useTranslation()
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('ai.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('ai.subtitle')}</p>
      </div>
    </div>
  )
}
