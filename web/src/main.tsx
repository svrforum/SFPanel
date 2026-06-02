import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './i18n'
import './lib/monaco'
import './index.css'
import App from './App'

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

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
