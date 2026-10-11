import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Download, Loader2, RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { CODE_MESSAGES } from '@/lib/codeMessages'
import { toastWarnings } from '@/lib/warnings'
import type { PackageUpdate as PackageInfo } from '@/types/api'
import { Button } from '@/components/ui/button'
import { StatusPill } from '@/components/StatusPill'
import type { AptTransactions } from '@/pages/packages/useAptTransaction'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

// apt update check + full/selective upgrade. An upgrade is a package
// transaction (useAptTransaction): apt runs in a unit of its own on the node
// and the dialog streams it, keeping the cluster ?node= scope.
export function SystemUpdatesCard({ tx }: { tx: AptTransactions }) {
  const { t } = useTranslation()
  const [updates, setUpdates] = useState<PackageInfo[]>([])
  const [lastChecked, setLastChecked] = useState<string | null>(null)
  const [selectedPackages, setSelectedPackages] = useState<Set<string>>(new Set())
  const [checking, setChecking] = useState(false)
  const [upgradingHere, setUpgradingHere] = useState(false)
  // Any transaction holds the buttons; the label is an upgrade's own.
  const busy = tx.busy
  const upgrading = upgradingHere || tx.running?.kind === 'upgrade'

  const handleCheckUpdates = useCallback(async () => {
    setChecking(true)
    try {
      const data = await api.checkUpdates()
      setUpdates(data.updates || [])
      setLastChecked(data.last_checked || new Date().toISOString())
      setSelectedPackages(new Set())
      if ((data.updates || []).length === 0) {
        toast.success(t('packages.noUpdatesAvailable'))
      } else {
        toast.info(t('packages.updatesFound', { count: data.updates.length }))
      }
      // A repository that could not be refreshed, while the others were:
      // Proxmox's enterprise repository without a subscription.
      toastWarnings(data.warnings, t)
    } catch (err: unknown) {
      const code = err instanceof Error ? (err as Error & { code?: string }).code : undefined
      if (code && CODE_MESSAGES[code]) {
        toast.error((err as Error).message)
        if (code === 'APT_TRANSACTION_RUNNING') tx.refresh()
      } else {
        const message = err instanceof Error ? err.message : ''
        toast.error(message ? t('packages.checkUpdatesFailed') + ': ' + message : t('packages.checkUpdatesFailed'))
      }
    } finally {
      setChecking(false)
    }
  }, [t, tx])

  const handleUpgradePackages = useCallback(
    async (packages?: string[]) => {
      setUpgradingHere(true)
      try {
        const done = await tx.upgrade(packages ?? [])
        if (done?.result === 'success') {
          setSelectedPackages(new Set())
          await handleCheckUpdates()
        }
      } finally {
        setUpgradingHere(false)
      }
    },
    [tx, handleCheckUpdates],
  )

  const togglePackageSelection = useCallback((name: string) => {
    setSelectedPackages((prev) => {
      const next = new Set(prev)
      if (next.has(name)) {
        next.delete(name)
      } else {
        next.add(name)
      }
      return next
    })
  }, [])

  const toggleSelectAll = useCallback(() => {
    setSelectedPackages((prev) => {
      if (prev.size === updates.length) {
        return new Set()
      }
      return new Set(updates.map((p) => p.name))
    })
  }, [updates])

  return (
    <div className="bg-card rounded-2xl card-shadow">
      <div className="px-6 pt-5 pb-4">
        <h3 className="text-[15px] font-semibold flex items-center gap-2">
          <RefreshCw className="h-4 w-4" aria-hidden="true" />
          {t('packages.systemUpdates')}
        </h3>
        <p className="text-[13px] text-muted-foreground mt-1">
          {lastChecked
            ? t('packages.lastChecked', {
                time: new Date(lastChecked).toLocaleString(),
              })
            : t('packages.neverChecked')}
        </p>
      </div>
      <div className="px-6 pb-5 space-y-4">
        {/* Action buttons */}
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            className="rounded-xl"
            onClick={handleCheckUpdates}
            disabled={checking || busy}
          >
            {checking ? (
              <>
                <Loader2 className="animate-spin" aria-hidden="true" />
                {t('packages.checking')}
              </>
            ) : (
              <>
                <RefreshCw aria-hidden="true" />
                {t('packages.checkForUpdates')}
              </>
            )}
          </Button>
          <Button
            className="rounded-xl"
            onClick={() => handleUpgradePackages()}
            disabled={updates.length === 0 || busy || checking}
          >
            {upgrading ? (
              <>
                <Loader2 className="animate-spin" aria-hidden="true" />
                {t('packages.upgrading')}
              </>
            ) : (
              <>
                <Download aria-hidden="true" />
                {t('packages.upgradeAll')}
              </>
            )}
          </Button>
          {selectedPackages.size > 0 && (
            <Button
              variant="secondary"
              className="rounded-xl"
              onClick={() => handleUpgradePackages(Array.from(selectedPackages))}
              disabled={busy || checking}
            >
              <Download aria-hidden="true" />
              {t('packages.upgradeSelected', { count: selectedPackages.size })}
            </Button>
          )}
          {updates.length > 0 && (
            <span className="text-[13px] text-muted-foreground ml-auto">
              {t('packages.updatesAvailable', { count: updates.length })}
            </span>
          )}
        </div>

        {/* Mobile updates card view */}
        <div className="md:hidden space-y-2">
          {checking ? (
            <div className="flex items-center justify-center gap-2 text-muted-foreground py-8">
              <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
              <span className="text-[13px]">{t('packages.checkingForUpdates')}</span>
            </div>
          ) : updates.length === 0 ? (
            <div className="text-center text-[13px] text-muted-foreground py-8">
              {t('packages.noUpdates')}
            </div>
          ) : (
            updates.map((pkg) => (
              <div key={pkg.name} className="bg-card rounded-2xl p-4 card-shadow">
                <div className="flex items-start gap-3">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-gray-300 accent-primary mt-0.5 shrink-0"
                    checked={selectedPackages.has(pkg.name)}
                    onChange={() => togglePackageSelection(pkg.name)}
                    aria-label={pkg.name}
                  />
                  <div className="min-w-0 flex-1">
                    <p className="text-[13px] font-medium font-mono truncate">{pkg.name}</p>
                    <div className="flex items-center gap-2 mt-1 flex-wrap">
                      <span className="text-[11px] text-muted-foreground font-mono">{pkg.current_version}</span>
                      <span className="text-[11px] text-muted-foreground">→</span>
                      <StatusPill tone="success">{pkg.new_version}</StatusPill>
                    </div>
                  </div>
                </div>
              </div>
            ))
          )}
        </div>

        {/* Desktop updates table */}
        <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-gray-300 accent-primary"
                    checked={updates.length > 0 && selectedPackages.size === updates.length}
                    onChange={toggleSelectAll}
                    disabled={updates.length === 0}
                    aria-label={t('packages.selectAll')}
                  />
                </TableHead>
                <TableHead>{t('packages.packageName')}</TableHead>
                <TableHead>{t('packages.currentVersion')}</TableHead>
                <TableHead>{t('packages.newVersion')}</TableHead>
                <TableHead>{t('packages.architecture')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {checking ? (
                <TableRow>
                  <TableCell colSpan={5} className="text-center py-8">
                    <div className="flex items-center justify-center gap-2 text-muted-foreground">
                      <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
                      <span className="text-[13px]">{t('packages.checkingForUpdates')}</span>
                    </div>
                  </TableCell>
                </TableRow>
              ) : updates.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={5}
                    className="text-center text-[13px] text-muted-foreground py-8"
                  >
                    {t('packages.noUpdates')}
                  </TableCell>
                </TableRow>
              ) : (
                updates.map((pkg) => (
                  <TableRow key={pkg.name}>
                    <TableCell>
                      <input
                        type="checkbox"
                        className="h-4 w-4 rounded border-gray-300 accent-primary"
                        checked={selectedPackages.has(pkg.name)}
                        onChange={() => togglePackageSelection(pkg.name)}
                        aria-label={pkg.name}
                      />
                    </TableCell>
                    <TableCell className="font-medium font-mono text-[13px]">
                      {pkg.name}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-[13px] font-mono">
                      {pkg.current_version}
                    </TableCell>
                    <TableCell className="text-[13px] font-mono">
                      <StatusPill tone="success">{pkg.new_version}</StatusPill>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-[13px]">
                      {pkg.arch}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>
    </div>
  )
}
