import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Globe } from 'lucide-react'
import { LANGUAGE_KEY } from '@/i18n'
import { LogoMark } from '@/components/Logo'

// Shared styling for the auth entry screens (Login/Setup/Connect) — these
// className strings used to be copy-pasted eight times across the three files.
export const AUTH_INPUT_CLASS =
  'h-11 rounded-xl bg-secondary/50 border-0 focus-visible:ring-2 focus-visible:ring-primary/30'
export const AUTH_SUBMIT_CLASS =
  'w-full h-11 rounded-xl font-semibold text-sm transition-all duration-200 hover:brightness-110'

// Error banner placed inside each screen's form (kept as a separate piece so
// it sits inside the form's space-y flow exactly where it always did).
export function AuthErrorBanner({ error }: { error: string }) {
  if (!error) return null
  return (
    <div className="bg-destructive/8 text-destructive text-sm p-3 rounded-xl text-center font-medium whitespace-pre-line">
      {error}
    </div>
  )
}

interface AuthShellProps {
  subtitle: string
  children: ReactNode
  /** Extra content below the card, inside the centered column (e.g. Connect's diagnostics panel). */
  below?: ReactNode
}

// Centered logo-header + card shell shared by the three pre-auth screens,
// including the ko/en switcher that previously existed only on Connect —
// web (non-Tauri) users otherwise had no way to change the detected language
// before signing in.
export default function AuthShell({ subtitle, children, below }: AuthShellProps) {
  const { i18n } = useTranslation()
  const currentLang = i18n.language?.startsWith('ko') ? 'ko' : 'en'

  const switchLanguage = (lang: string) => {
    i18n.changeLanguage(lang)
    localStorage.setItem(LANGUAGE_KEY, lang)
  }

  return (
    <div className="min-h-screen flex flex-col items-center justify-center bg-background">
      <div className="w-full max-w-sm px-6">
        <div className="text-center mb-8">
          <LogoMark className="mx-auto mb-3 h-16 w-16" />
          <h1 className="text-2xl font-bold tracking-tight text-foreground">SFPanel</h1>
          <p className="text-sm text-muted-foreground mt-2">{subtitle}</p>
        </div>

        <div className="bg-card rounded-2xl card-shadow-lg p-8">{children}</div>

        {below}
      </div>

      <div className="flex items-center gap-2 mt-6">
        <Globe className="w-3.5 h-3.5 text-muted-foreground" />
        <button
          type="button"
          onClick={() => switchLanguage('ko')}
          className={`text-[12px] px-2 py-0.5 rounded-full transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
            currentLang === 'ko'
              ? 'bg-primary/10 text-primary font-medium'
              : 'text-muted-foreground hover:text-foreground'
          }`}
        >
          한국어
        </button>
        <span className="text-muted-foreground text-[11px]">|</span>
        <button
          type="button"
          onClick={() => switchLanguage('en')}
          className={`text-[12px] px-2 py-0.5 rounded-full transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
            currentLang === 'en'
              ? 'bg-primary/10 text-primary font-medium'
              : 'text-muted-foreground hover:text-foreground'
          }`}
        >
          English
        </button>
      </div>
    </div>
  )
}
