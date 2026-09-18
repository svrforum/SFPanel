import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { setApiTranslator } from '@/lib/api'
import ko from './locales/ko.json'
import en from './locales/en.json'

const LANGUAGE_KEY = 'sfpanel_language'

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    fallbackLng: 'en',
    supportedLngs: ['en', 'ko'],
    resources: {
      ko: { translation: ko },
      en: { translation: en },
    },
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: LANGUAGE_KEY,
      caches: ['localStorage'],
    },
    interpolation: {
      escapeValue: false,
    },
  })

// The API client can't call useTranslation() — it isn't a component — so hand
// it the initialised instance. Without this its network-failure message stays
// on the English fallback baked into the module.
setApiTranslator((key, fallback) => i18n.t(key, { defaultValue: fallback }))

export default i18n
export { LANGUAGE_KEY }
