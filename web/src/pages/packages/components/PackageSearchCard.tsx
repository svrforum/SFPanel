import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Download, Loader2, Package, Search, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { PackageSearchResult } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { StatusPill } from '@/components/StatusPill'
import type { SSEOutput } from '@/components/OutputDialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

// apt search + install/remove. These are unary API calls (not SSE); their
// captured output is still shown in the shared output dialog for parity with
// the streaming operations.
export function PackageSearchCard({ output }: { output: SSEOutput }) {
  const { t } = useTranslation()
  const [searchQuery, setSearchQuery] = useState('')
  const [searchResults, setSearchResults] = useState<PackageSearchResult[]>([])
  const [hasSearched, setHasSearched] = useState(false)
  const [searching, setSearching] = useState(false)
  const [installName, setInstallName] = useState<string | null>(null)
  const [removeName, setRemoveName] = useState<string | null>(null)

  const handleSearch = useCallback(async () => {
    if (!searchQuery.trim()) return
    setSearching(true)
    setHasSearched(true)
    try {
      const data = await api.searchPackages(searchQuery.trim())
      setSearchResults(data.packages || [])
      if ((data.packages || []).length === 0) {
        toast.info(t('packages.noSearchResults'))
      }
    } catch {
      toast.error(t('packages.searchFailed'))
    } finally {
      setSearching(false)
    }
  }, [searchQuery, t])

  const handleInstallPackage = useCallback(
    async (name: string) => {
      setInstallName(name)
      output.openOutput(t('packages.installingPackage', { name }))
      try {
        output.appendOutput(t('packages.installStarted', { name }) + '\n')
        const result = (await api.installPackage(name)) as { output?: string }
        if (result?.output) {
          output.appendOutput(result.output)
        }
        output.appendOutput('\n' + t('packages.installSuccess', { name }) + '\n')
        output.finishOutput()
        toast.success(t('packages.installSuccess', { name }))
        // Update search results to reflect installed status
        setSearchResults((prev) =>
          prev.map((pkg) => (pkg.name === name ? { ...pkg, installed: true } : pkg)),
        )
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : t('packages.installFailed', { name })
        output.appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
        output.finishOutput()
        toast.error(message)
      } finally {
        setInstallName(null)
      }
    },
    [output, t],
  )

  const handleRemovePackage = useCallback(
    async (name: string) => {
      setRemoveName(name)
      output.openOutput(t('packages.removingPackage', { name }))
      try {
        output.appendOutput(t('packages.removeStarted', { name }) + '\n')
        const result = (await api.removePackage(name)) as { output?: string }
        if (result?.output) {
          output.appendOutput(result.output)
        }
        output.appendOutput('\n' + t('packages.removeSuccess', { name }) + '\n')
        output.finishOutput()
        toast.success(t('packages.removeSuccess', { name }))
        // Update search results to reflect removed status
        setSearchResults((prev) =>
          prev.map((pkg) => (pkg.name === name ? { ...pkg, installed: false } : pkg)),
        )
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : t('packages.removeFailed', { name })
        output.appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
        output.finishOutput()
        toast.error(message)
      } finally {
        setRemoveName(null)
      }
    },
    [output, t],
  )

  return (
    <div className="bg-card rounded-2xl card-shadow">
      <div className="px-6 pt-5 pb-4">
        <h3 className="text-[15px] font-semibold flex items-center gap-2">
          <Package className="h-4 w-4" aria-hidden="true" />
          {t('packages.searchAndInstall')}
        </h3>
        <p className="text-[13px] text-muted-foreground mt-1">
          {t('packages.searchDescription')}
        </p>
      </div>
      <div className="px-6 pb-5 space-y-4">
        {/* Search bar */}
        <div className="flex items-center gap-2 max-w-xl">
          <div className="relative flex-1">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" aria-hidden="true" />
            <Input
              className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
              placeholder={t('packages.searchPlaceholder')}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !searching) handleSearch()
              }}
              disabled={searching}
            />
          </div>
          <Button
            className="rounded-xl"
            onClick={handleSearch}
            disabled={searching || !searchQuery.trim()}
          >
            {searching ? (
              <Loader2 className="animate-spin" aria-hidden="true" />
            ) : (
              <Search aria-hidden="true" />
            )}
            {t('packages.search')}
          </Button>
        </div>

        {/* Search results */}
        {searchResults.length > 0 && (
          <>
            {/* Mobile search results */}
            <div className="md:hidden space-y-2">
              {searchResults.map((pkg) => (
                <div key={pkg.name} className="bg-card rounded-2xl p-4 card-shadow">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 flex-wrap">
                        <p className="text-[13px] font-medium font-mono">{pkg.name}</p>
                        {pkg.installed && (
                          <StatusPill tone="success">{t('packages.installed')}</StatusPill>
                        )}
                      </div>
                      {pkg.description && (
                        <p className="text-[11px] text-muted-foreground mt-0.5 line-clamp-2">{pkg.description}</p>
                      )}
                    </div>
                    <div className="shrink-0">
                      {!pkg.installed ? (
                        <Button
                          size="sm"
                          className="rounded-xl"
                          onClick={() => handleInstallPackage(pkg.name)}
                          disabled={installName === pkg.name}
                          aria-label={t('packages.install')}
                        >
                          {installName === pkg.name ? (
                            <Loader2 className="animate-spin h-4 w-4" aria-hidden="true" />
                          ) : (
                            <Download className="h-4 w-4" aria-hidden="true" />
                          )}
                        </Button>
                      ) : (
                        <Button
                          size="sm"
                          variant="destructive"
                          className="rounded-xl"
                          onClick={() => handleRemovePackage(pkg.name)}
                          disabled={removeName === pkg.name}
                          aria-label={t('packages.remove')}
                        >
                          {removeName === pkg.name ? (
                            <Loader2 className="animate-spin h-4 w-4" aria-hidden="true" />
                          ) : (
                            <Trash2 className="h-4 w-4" aria-hidden="true" />
                          )}
                        </Button>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>

            {/* Desktop search results */}
            <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('packages.packageName')}</TableHead>
                    <TableHead>{t('packages.description')}</TableHead>
                    <TableHead className="text-right">{t('packages.actions')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {searchResults.map((pkg) => (
                    <TableRow key={pkg.name}>
                      <TableCell className="font-medium font-mono text-[13px]">
                        <div className="flex items-center gap-2">
                          {pkg.name}
                          {pkg.installed && (
                            <StatusPill tone="success">{t('packages.installed')}</StatusPill>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-muted-foreground text-[13px] max-w-md truncate">
                        {pkg.description}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-1">
                          {!pkg.installed ? (
                            <Button
                              size="sm"
                              className="rounded-xl"
                              onClick={() => handleInstallPackage(pkg.name)}
                              disabled={installName === pkg.name}
                            >
                              {installName === pkg.name ? (
                                <>
                                  <Loader2 className="animate-spin" aria-hidden="true" />
                                  {t('packages.installing')}
                                </>
                              ) : (
                                <>
                                  <Download aria-hidden="true" />
                                  {t('packages.install')}
                                </>
                              )}
                            </Button>
                          ) : (
                            <Button
                              size="sm"
                              variant="destructive"
                              className="rounded-xl"
                              onClick={() => handleRemovePackage(pkg.name)}
                              disabled={removeName === pkg.name}
                            >
                              {removeName === pkg.name ? (
                                <>
                                  <Loader2 className="animate-spin" aria-hidden="true" />
                                  {t('packages.removing')}
                                </>
                              ) : (
                                <>
                                  <Trash2 aria-hidden="true" />
                                  {t('packages.remove')}
                                </>
                              )}
                            </Button>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </>
        )}

        {/* Empty search state */}
        {searchResults.length === 0 && !searching && hasSearched && (
          <div className="text-center text-[13px] text-muted-foreground py-6">
            {t('packages.noSearchResults')}
          </div>
        )}
      </div>
    </div>
  )
}
