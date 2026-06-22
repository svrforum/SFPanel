// Theme activation. The token system in index.css ships a complete `.dark`
// palette, but nothing ever toggled the `.dark` class — so dark mode was dead
// and every dark token unused. This module is the single owner of that toggle.
//
// Precedence: an explicit user choice ('light' | 'dark') stored in localStorage
// wins; otherwise we follow the OS via prefers-color-scheme ('system', the
// default) and live-update when the OS flips. The first paint is handled by a
// tiny inline script in index.html (see THEME_PREPAINT) so there's no
// light-to-dark flash before this module loads.

export type ThemePref = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'sfpanel-theme'

export function getThemePref(): ThemePref {
  const v = localStorage.getItem(STORAGE_KEY)
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system'
}

function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

export function resolveTheme(pref: ThemePref): 'light' | 'dark' {
  return pref === 'system' ? (systemPrefersDark() ? 'dark' : 'light') : pref
}

function apply(pref: ThemePref) {
  const dark = resolveTheme(pref) === 'dark'
  document.documentElement.classList.toggle('dark', dark)
  // Keep the iOS/Android status-bar colour in step with the surface colour.
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', dark ? '#0d1117' : '#3182f6')
}

export function setThemePref(pref: ThemePref) {
  localStorage.setItem(STORAGE_KEY, pref)
  apply(pref)
  window.dispatchEvent(new CustomEvent('sfpanel:themechange'))
}

// initTheme is called once at startup. It re-applies the stored preference (the
// inline pre-paint script already set the class; this keeps them in sync) and
// attaches a listener so a live OS theme switch is honoured while in 'system'.
export function initTheme() {
  apply(getThemePref())
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (getThemePref() === 'system') {
      apply('system')
      window.dispatchEvent(new CustomEvent('sfpanel:themechange'))
    }
  })
}
