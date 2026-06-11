import { expect, type APIRequestContext } from '@playwright/test'

// Shared auth helper for the e2e suite.
//
// Credentials: every spec reads PW_USER / PW_PASS, defaulting to the CI seed
// account (admin / TestPass123!) created via POST /api/v1/auth/setup.
// NOTE: the account must have NO TOTP enrolled — login() posts username +
// password only and cannot answer a 2FA challenge.
//
// CSRF: since 2026-05-15 the panel enforces double-submit CSRF on every
// non-GET request that carries any sfpanel cookie. Playwright's request
// contexts keep a cookie jar, so the Set-Cookie pair minted by /auth/login
// rides along on every later call — which means Bearer-only POSTs 403 unless
// they ALSO echo the sfpanel_csrf cookie via X-CSRF-Token. login() captures
// the cookie from the jar and bakes the echo into `headers`.

export const PW_USER = process.env.PW_USER || 'admin'
export const PW_PASS = process.env.PW_PASS || 'TestPass123!'

const CSRF_COOKIE = 'sfpanel_csrf'
const CSRF_HEADER = 'X-CSRF-Token'

export interface AuthSession {
  token: string
  csrf: string
  /** Bearer + Content-Type + X-CSRF-Token — safe for both GET and non-GET calls. */
  headers: Record<string, string>
}

export interface LoginOptions {
  /** Absolute panel URL. Omit to use the request context's configured baseURL. */
  baseURL?: string
  username?: string
  password?: string
}

// Logs in via POST /api/v1/auth/login and returns the access token plus
// headers carrying the CSRF double-submit echo. The login response's
// Set-Cookie lands in `request`'s cookie jar automatically.
export async function login(request: APIRequestContext, opts: LoginOptions = {}): Promise<AuthSession> {
  const base = opts.baseURL ?? ''
  const res = await request.post(`${base}/api/v1/auth/login`, {
    headers: { 'Content-Type': 'application/json' },
    data: { username: opts.username ?? PW_USER, password: opts.password ?? PW_PASS },
  })
  const json = await res.json()
  expect(json.success, `login failed: ${JSON.stringify(json)}`).toBe(true)
  const token = json.data.token as string
  const csrf = await csrfCookieValue(request, base)
  const headers: Record<string, string> = {
    Authorization: `Bearer ${token}`,
    'Content-Type': 'application/json',
  }
  // No cookie means refresh-token issuance failed server-side; the Bearer-
  // with-zero-cookies CSRF bypass applies then, so omitting the echo is fine.
  if (csrf) headers[CSRF_HEADER] = csrf
  return { token, csrf, headers }
}

// Reads the current sfpanel_csrf value from the context's cookie jar,
// scoped to the target host when one is given. Cookies ignore ports, so
// two panels on the same hostname (multi-port lab setups) overwrite each
// other's cookie — log in against the node you're about to POST to last.
async function csrfCookieValue(request: APIRequestContext, base: string): Promise<string> {
  const { cookies } = await request.storageState()
  let host: string | null = null
  if (base) {
    try {
      host = new URL(base).hostname
    } catch {
      host = null
    }
  }
  const match = cookies.find(
    (c) => c.name === CSRF_COOKIE && (!host || c.domain.replace(/^\./, '') === host),
  )
  return match?.value ?? ''
}
