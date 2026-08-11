import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Download, Loader2, RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { PackageUpdate as PackageInfo } from '@/types/api'
import { Button } from '@/components/ui/button'
import { StatusPill } from '@/components/StatusPill'
import { streamErrorMessage, type SSEOutput } from '@/components/OutputDialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

// apt update check + full/selective upgrade (SSE streamed into the shared
// output dialog; postTextStream keeps the cluster ?node= scope).
export function SystemUpdatesCard({ output }: { output: SSEOutput }) {
  const { t } = useTranslation()
  const [updates, setUpdates] = useState<PackageInfo[]>([])
  const [lastChecked, setLastChecked] = useState<string | null>(null)
  const [selectedPackages, setSelectedPackages] = useState<Set<string>>(new Set())
  const [checking, setChecking] = useState(false)
  const [upgrading, setUpgrading] = useState(false)

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
    } catch {
      toast.error(t('packages.checkUpdatesFailed'))
    } finally {
      setChecking(false)
    }
  }, [t])

  const handleUpgradePackages = useCallback(
    async (packages?: string[]) => {
      const label = packages
        ? t('packages.upgradingSelected', { count: packages.length })
        : t('packages.upgradingAll')
      setUpgrading(true)
      output.openOutput(label)
      try {
        output.appendOutput(label + '...\n')
        // /packages/upgrade streams via SSE — a full distro upgrade routinely
        // runs longer than the legacy 5 min unary cap. The server's [DONE]
        // marker is ignored (finishOnDone: false); the stream ends naturally
        // on EOF and we append our own closing line.
        await output.runStream('/packages/upgrade', {
          body: { packages: packages ?? [] },
          finishOnDone: false,
        })
        output.appendOutput('\n' + t('packages.upgradeComplete') + '\n')
        output.finishOutput()
        toast.success(t('packages.upgradeComplete'))
        setSelectedPackages(new Set())
        await handleCheckUpdates()
      } catch (err: unknown) {
        const message = streamErrorMessage(err, t('packages.streamStartFailed'))
        output.appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
        output.finishOutput()
        toast.error(message)
      } finally {
        setUpgrading(false)
      }
    },
    [output, handleCheckUpdates, t],
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
            disabled={checking || upgrading}
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
            disabled={updates.length === 0 || upgrading || checking}
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
              disabled={upgrading || checking}
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
