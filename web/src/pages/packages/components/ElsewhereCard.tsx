import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { useServerScope } from '@/hooks/useServerScope'

// What is installed on the screen that uses it rather than here: product
// names, untranslated, and the menu path to that screen. `where` takes t so
// each key is a literal call the locale check can find.
const ELSEWHERE: { what: string; to: string; where: (t: TFunction) => string }[] = [
  { what: 'tmux · Claude Code · Codex · Gemini CLI', to: '/terminal', where: (t) => t('layout.nav.terminal') },
  { what: 'WireGuard', to: '/network/wireguard', where: (t) => `${t('layout.nav.networkVpn')} › ${t('network.sidebar.wireguard')}` },
  { what: 'Tailscale', to: '/network/tailscale', where: (t) => `${t('layout.nav.networkVpn')} › ${t('network.sidebar.tailscale')}` },
  { what: 'Fail2ban', to: '/firewall/fail2ban', where: (t) => `${t('layout.nav.firewall')} › ${t('firewall.tabs.fail2ban')}` },
  { what: 'smartmontools', to: '/disk/overview', where: (t) => `${t('layout.nav.disk')} › ${t('disk.tabs.overview')}` },
  { what: 'cifs-utils · nfs-common', to: '/disk/network-shares', where: (t) => `${t('layout.nav.disk')} › ${t('disk.tabs.shares')}` },
  { what: 'nmap', to: '/map', where: (t) => t('layout.nav.map') },
]

/**
 * The tools this page does not install, each with a link to the screen that
 * does. The links go as the menu's do: in a cluster a server's screen names
 * the server shown, and the network map, about every server, names none.
 */
export function ElsewhereCard() {
  const { t } = useTranslation()
  const { href } = useServerScope()
  return (
    <section aria-labelledby="packages-elsewhere-title" className="bg-card rounded-2xl card-shadow">
      <div className="px-6 pt-5 pb-4">
        <h3 id="packages-elsewhere-title" className="text-[15px] font-semibold">{t('packages.elsewhere.title')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1">{t('packages.elsewhere.description')}</p>
      </div>
      <ul className="px-6 pb-5 space-y-2 text-[13px]">
        {ELSEWHERE.map(({ what, to, where }) => (
          <li key={to}>
            <span className="font-mono">{what}</span> <span className="text-muted-foreground" aria-hidden="true">→</span>{' '}
            <Link to={href(to)} className="text-primary hover:underline">{where(t)}</Link>
          </li>
        ))}
      </ul>
    </section>
  )
}
