import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ShieldCheck, Download, AlertCircle } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { ListLoadState } from '@/components/ListLoadState'
import { api } from '@/lib/api'
import { downloadBlob } from '@/lib/utils'
import type { TLSStatus } from '@/types/api'

function formatDate(iso: string | undefined, locale: string) {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString(locale)
}

// TLSCertificate shows the panel's own certificate and offers its certificate
// authority for download.
//
// It lives in the "system" settings tab because certificate material is
// per-node, not cluster-wide: each node runs its own authority, so browsing a
// remote node with ?node= must show — and hand out — that node's CA. The API
// client's withNode() is what carries that through the raw blob fetch.
export default function TLSCertificate() {
  const { t, i18n } = useTranslation()
  const [status, setStatus] = useState<TLSStatus | null>(null)
  // A read that failed: the section used to vanish as if the panel served
  // plain HTTP, and the authority could not be fetched from here.
  const [loadError, setLoadError] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [downloading, setDownloading] = useState(false)

  useEffect(() => {
    let cancelled = false
    api
      .getTLSStatus()
      .then((s) => {
        if (!cancelled) setStatus(s)
      })
      .catch((err: unknown) => {
        if (!cancelled) setLoadError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [attempt])

  const retry = useCallback(() => {
    setLoadError(null)
    setAttempt((n) => n + 1)
  }, [])

  const handleDownloadCA = async () => {
    setDownloading(true)
    try {
      const blob = await api.downloadCACert()
      downloadBlob(blob, 'sfpanel-ca.crt')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setDownloading(false)
    }
  }

  if (loadError) {
    return (
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h2 className="text-[15px] font-semibold mb-3">{t('settings.tls.title', { defaultValue: 'HTTPS certificate' })}</h2>
        <ListLoadState loading={false} error={loadError} errorTitle={t('settings.tls.loadFailed')} onRetry={retry} />
      </div>
    )
  }

  // Nothing to say on a panel that serves plain HTTP, and nothing to hand out
  // on one whose certificate the operator supplied themselves.
  if (!status?.enabled || !status.managed) return null

  const expiringSoon = (status.days_until_renew ?? 999) <= 0

  return (
    <div className="bg-card rounded-2xl p-6 card-shadow">
      <h2 className="text-[15px] font-semibold flex items-center gap-2">
        <ShieldCheck className="h-4 w-4 text-success" aria-hidden="true" />
        {t('settings.tls.title', { defaultValue: 'HTTPS certificate' })}
      </h2>
      <p className="text-[13px] text-muted-foreground mt-1 mb-4">
        {t('settings.tls.description', {
          defaultValue:
            'This panel issues its own certificate. Install the authority below on each device once and the browser warning stops for good.',
        })}
      </p>

      <div className="bg-secondary/40 rounded-xl p-3 mb-4 space-y-2">
        <Row label={t('settings.tls.authority', { defaultValue: 'Authority' })} value={status.ca_subject || '—'} />
        <Row
          label={t('settings.tls.caExpires', { defaultValue: 'Authority expires' })}
          value={formatDate(status.ca_not_after, i18n.language)}
        />
        <Row
          label={t('settings.tls.certExpires', { defaultValue: 'Certificate expires' })}
          value={formatDate(status.not_after, i18n.language)}
        />
        {status.ca_fingerprint && (
          <div className="pt-1">
            <p className="text-[12px] text-muted-foreground uppercase tracking-wider mb-1">
              {t('settings.tls.fingerprint', { defaultValue: 'SHA-256 fingerprint' })}
            </p>
            {/* Shown so an operator can match what the panel serves against what
                their OS certificate viewer displays after installing it. */}
            <code className="text-[12px] font-mono break-all text-foreground/80">{status.ca_fingerprint}</code>
          </div>
        )}
        {(status.dns_names?.length || status.ip_addresses?.length) && (
          <div className="pt-1">
            <p className="text-[12px] text-muted-foreground uppercase tracking-wider mb-1">
              {t('settings.tls.validFor', { defaultValue: 'Valid for' })}
            </p>
            <div className="flex flex-wrap gap-1">
              {[...(status.dns_names ?? []), ...(status.ip_addresses ?? [])].map((name) => (
                <span key={name} className="text-[12px] font-mono bg-secondary rounded px-1.5 py-0.5">
                  {name}
                </span>
              ))}
            </div>
          </div>
        )}
      </div>

      <p className="text-[12px] text-muted-foreground mb-4 flex items-start gap-1">
        <AlertCircle className="h-3 w-3 shrink-0 mt-0.5" aria-hidden="true" />
        {t('settings.tls.renewalNote', {
          defaultValue:
            'The certificate renews itself on restart before it expires, and reissues if this host’s addresses change. The authority does not change, so devices only ever install it once.',
        })}
      </p>

      {expiringSoon && (
        <p className="text-[12px] text-warning mb-4">
          {t('settings.tls.renewPending', {
            defaultValue: 'Renewal is due — it happens on the next restart.',
          })}
        </p>
      )}

      <Button onClick={handleDownloadCA} disabled={downloading} variant="outline" className="rounded-xl">
        <Download className="h-4 w-4 mr-2" />
        {downloading
          ? t('settings.tls.downloading', { defaultValue: 'Downloading…' })
          : t('settings.tls.downloadCA', { defaultValue: 'Download authority (ca.crt)' })}
      </Button>
    </div>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-[12px] text-muted-foreground shrink-0">{label}</span>
      <span className="text-[12px] text-foreground/80 text-right break-all">{value}</span>
    </div>
  )
}
