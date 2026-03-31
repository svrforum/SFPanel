import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './i18n'
import './lib/monaco'
import './index.css'
import App from './App'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
