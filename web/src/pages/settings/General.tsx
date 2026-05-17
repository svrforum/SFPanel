import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'

export default function General() {
  const { t, i18n } = useTranslation()

  return (
    <div className="space-y-6 mt-6">
      {/* Server Connection (Tauri only) */}
      {api.isTauri && api.serverUrl && (
        <div className="bg-card rounded-2xl card-shadow p-5 mb-6">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-[15px] font-semibold">{t('settings.connectedServer')}</p>
              <p className="text-[13px] text-muted-foreground mt-1">{api.serverUrl}</p>
            </div>
            <Button
              variant="outline"
              className="rounded-xl text-[13px]"
              onClick={() => {
                api.setServerUrl(null)
                api.clearToken()
                window.location.href = '/connect'
              }}
            >
              {t('settings.disconnectServer')}
            </Button>
          </div>
        </div>
      )}

      {/* Language */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.language')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.languageDescription')}</p>
        <div className="flex items-center gap-2">
          <Button
            variant={i18n.language === 'en' ? 'default' : 'outline'}
            size="sm"
            onClick={() => i18n.changeLanguage('en')}
            className="rounded-xl"
          >
            English
          </Button>
          <Button
            variant={i18n.language?.startsWith('ko') ? 'default' : 'outline'}
            size="sm"
            onClick={() => i18n.changeLanguage('ko')}
            className="rounded-xl"
          >
            한국어
          </Button>
        </div>
      </div>
    </div>
  )
}
