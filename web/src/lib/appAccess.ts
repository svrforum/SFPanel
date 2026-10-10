import type { AppStoreMeta } from '@/types/api'

// Where an installed app answers: its own scheme (http unless the catalog says
// the app serves TLS itself) on the host the panel was reached by. Never the
// panel's protocol: a panel on HTTPS sent every plain-HTTP app to https://,
// and a panel behind a plain-HTTP proxy sent the one TLS app to http://.
export function appAccessUrl(app: Pick<AppStoreMeta, 'scheme'>, hostname: string, port: string): string {
  return `${app.scheme === 'https' ? 'https' : 'http'}://${hostname}:${port}`
}
