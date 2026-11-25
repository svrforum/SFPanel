// Remove in the release after the one that moved these: performance tuning
// to /tuning, the terminal timeout to the terminal's tools sheet, the upload
// limit to the file manager's upload menu.
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'

const MOVED = [
  { to: '/tuning', key: 'settings.moved.tuning' },
  { to: '/terminal', key: 'settings.moved.terminal' },
  { to: '/files', key: 'settings.moved.upload' },
] as const

/** Where the settings this tab used to hold went, each sentence a link there. */
export default function MovedSettings() {
  const { t } = useTranslation()
  return (
    <section aria-labelledby="moved-settings-title" className="bg-card rounded-2xl p-6 card-shadow mt-6">
      <h2 id="moved-settings-title" className="text-[15px] font-semibold">{t('settings.moved.title')}</h2>
      <ul className="mt-3 space-y-2">
        {MOVED.map(({ to, key }) => (
          <li key={to}>
            <Link to={to} className="text-[13px] text-primary hover:underline">{t(key)}</Link>
          </li>
        ))}
      </ul>
    </section>
  )
}
