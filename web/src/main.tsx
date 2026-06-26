import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './i18n'
import './index.css'
import { initTheme } from './lib/theme'
import App from './App'

// Activate theming: re-sync the stored preference applied by the index.html
// pre-paint script and start honouring live OS theme changes.
initTheme()

// After a panel upgrade, an already-open tab may still reference old lazy-chunk
// hashes that the upgraded server no longer has (they now 404). Vite fires
// `vite:preloadError` when a dynamic import fails to preload; recover by
// reloading once to pick up the fresh index.html + chunks. A timestamp guard
// caps this to one reload per 10s so a genuinely missing chunk can't loop.
window.addEventListener('vite:preloadError', (e) => {
  const KEY = 'sfpanel:chunk-reload-at'
  const last = Number(sessionStorage.getItem(KEY) || 0)
  if (Date.now() - last < 10000) return
  sessionStorage.setItem(KEY, String(Date.now()))
  e.preventDefault()
  window.location.reload()
})

// Auto-reload to the newest deployed build. The service worker uses
// registerType 'autoUpdate' (skipWaiting + clientsClaim), so a freshly deployed
// version takes control of this tab and fires 'controllerchange'; reload so the
// page runs the new assets instead of a stale cached shell. Guarded against the
// first install (no prior controller) and reload loops. The 60s poll lets a
// long-open tab pick up a deploy without the user manually refreshing.
if ('serviceWorker' in navigator) {
  const hadController = !!navigator.serviceWorker.controller
  let reloading = false
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (reloading || !hadController) return
    reloading = true
    window.location.reload()
  })
  navigator.serviceWorker.ready
    .then((reg) => { setInterval(() => { void reg.update() }, 60_000) })
    .catch(() => {})
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
