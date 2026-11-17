import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertCircle, CheckCircle2, Download, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'

// One card in the Dev Tools grid. Adding a CLI is a new entry in the TOOLS
// array in DevToolsCard instead of another ~50-line copy of this markup.
// The brand color arrives as a hex string, so the badge uses inline styles
// (Tailwind can't compile dynamic arbitrary-value classes); the "1a" suffix
// is the same 10% alpha the old bg-[#…]/10 classes produced.
export function DevToolCard({
  title,
  subtitle,
  accentColor,
  initial,
  checking,
  installed,
  statusError,
  onRetry,
  version,
  installLabel,
  installing,
  onInstall,
  installDisabled,
  footnote,
  installedExtra,
}: {
  title: string
  subtitle: string
  /** Brand hex color for the initial badge, e.g. '#68a063'. */
  accentColor: string
  initial: string
  checking: boolean
  installed: boolean
  /** Set when the status could not be read ('' when no reason came back). */
  statusError?: string | null
  onRetry?: () => void
  version?: string
  installLabel: string
  installing: boolean
  onInstall: () => void
  installDisabled?: boolean
  /** Shown under the install button when the tool can't be installed yet. */
  footnote?: string
  /** Custom installed-state content (Node.js: npm version + manage button). */
  installedExtra?: ReactNode
}) {
  const { t } = useTranslation()
  return (
    <div className="border rounded-xl p-4 space-y-3">
      <div className="flex items-center gap-2">
        <div
          className="h-8 w-8 rounded-lg flex items-center justify-center"
          style={{ backgroundColor: `${accentColor}1a` }}
        >
          <span className="text-[14px] font-bold" style={{ color: accentColor }}>{initial}</span>
        </div>
        <div>
          <p className="text-[13px] font-semibold">{title}</p>
          <p className="text-[12px] text-muted-foreground">{subtitle}</p>
        </div>
      </div>
      {checking ? (
        <div className="flex items-center gap-2 text-muted-foreground">
          <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
          <span className="text-[12px]">{t('packages.checking')}</span>
        </div>
      ) : statusError != null ? (
        <div className="space-y-2">
          <div className="flex items-start gap-1.5 text-muted-foreground">
            <AlertCircle className="h-3.5 w-3.5 mt-0.5 shrink-0" aria-hidden="true" />
            <span className="text-[12px] break-words">
              {statusError ? `${t('packages.toolStatusUnknown')}: ${statusError}` : t('packages.toolStatusUnknown')}
            </span>
          </div>
          {onRetry && (
            <Button size="sm" variant="outline" className="rounded-xl w-full" onClick={onRetry}>
              {t('common.retry')}
            </Button>
          )}
        </div>
      ) : installed ? (
        installedExtra ?? (
          <div className="flex items-center gap-1.5">
            <CheckCircle2 className="h-3.5 w-3.5 text-success" aria-hidden="true" />
            <span className="text-[12px] font-medium font-mono">{version || t('packages.installed')}</span>
          </div>
        )
      ) : (
        <Button
          size="sm"
          className="rounded-xl w-full"
          onClick={onInstall}
          disabled={installing || installDisabled}
          title={installDisabled && footnote ? footnote : ''}
        >
          {installing ? (
            <>
              <Loader2 className="animate-spin" aria-hidden="true" />
              {t('packages.installing')}
            </>
          ) : (
            <>
              <Download aria-hidden="true" />
              {installLabel}
            </>
          )}
        </Button>
      )}
      {footnote && !installed && statusError == null && (
        <p className="text-[12px] text-muted-foreground">{footnote}</p>
      )}
    </div>
  )
}
