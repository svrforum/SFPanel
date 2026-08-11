import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Download, Loader2, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { StatusPill } from '@/components/StatusPill'
import { streamErrorMessage, type SSEOutput } from '@/components/OutputDialog'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import type { DevToolStatus } from '@/pages/packages/components/DevToolsCard'

// NVM-backed Node.js version management: list/switch/remove installed
// versions and install remote LTS releases (SSE streamed into the shared
// output dialog; postTextStream keeps the cluster ?node= scope).
export function NodeVersionDialog({
  open,
  onOpenChange,
  nodeStatus,
  nvmInstalling,
  onInstallNvm,
  onNodeChanged,
  output,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  nodeStatus: DevToolStatus | null
  nvmInstalling: boolean
  onInstallNvm: () => void
  /** Called after a version install/switch/remove so the parent refreshes node status. */
  onNodeChanged: () => Promise<void> | void
  output: SSEOutput
}) {
  const { t } = useTranslation()
  const [nodeVersions, setNodeVersions] = useState<{ version: string; active: boolean; lts: boolean }[]>([])
  const [remoteLTS, setRemoteLTS] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [switching, setSwitching] = useState<string | null>(null)
  const [installingVersion, setInstallingVersion] = useState<string | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)

  const fetchNodeVersions = useCallback(async () => {
    setLoading(true)
    try {
      const data = await api.getNodeVersions()
      setNodeVersions(data.versions || [])
      setRemoteLTS(data.remote_lts || [])
    } catch {
      toast.error(t('packages.nodeVersionsFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    if (open) fetchNodeVersions()
  }, [open, fetchNodeVersions])

  const handleSwitch = useCallback(async (version: string) => {
    setSwitching(version)
    try {
      await api.switchNodeVersion(version)
      toast.success(t('packages.nodeSwitched', { version }))
      await fetchNodeVersions()
      await onNodeChanged()
    } catch {
      toast.error(t('packages.nodeSwitchFailed'))
    } finally {
      setSwitching(null)
    }
  }, [fetchNodeVersions, onNodeChanged, t])

  const handleInstallVersion = useCallback(async (version: string) => {
    setInstallingVersion(version)
    output.openOutput(t('packages.installingNodeVersion', { version }))
    try {
      await output.runStream('/packages/node-install-version', { body: { version } })
      output.finishOutput()
      toast.success(t('packages.nodeVersionInstalled', { version }))
      await fetchNodeVersions()
      await onNodeChanged()
    } catch (err: unknown) {
      const message = streamErrorMessage(err, t('packages.streamStartFailed'))
      output.appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
      output.finishOutput()
      toast.error(message)
    } finally {
      setInstallingVersion(null)
    }
  }, [output, fetchNodeVersions, onNodeChanged, t])

  const handleUninstallVersion = useCallback(async (version: string) => {
    setRemoving(version)
    try {
      await api.uninstallNodeVersion(version)
      toast.success(t('packages.nodeVersionRemoved', { version }))
      await fetchNodeVersions()
      await onNodeChanged()
    } catch {
      toast.error(t('packages.nodeVersionRemoveFailed'))
    } finally {
      setRemoving(null)
    }
  }, [fetchNodeVersions, onNodeChanged, t])

  const nvmInstalled = !!(nodeStatus as (DevToolStatus & { nvm_installed?: boolean }) | null)?.nvm_installed

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('packages.nodeVersionManage')}</DialogTitle>
          <DialogDescription>{t('packages.nodeVersionDescription')}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 max-h-96 overflow-y-auto">
          {loading ? (
            <div className="flex items-center justify-center py-6 gap-2 text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" />
              <span className="text-[13px]">{t('packages.checking')}</span>
            </div>
          ) : (
            <>
              {/* Installed versions */}
              <div className="space-y-2">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('packages.nodeInstalledVersions')}</p>
                {nodeVersions.length === 0 && !nvmInstalled ? (
                  <div className="space-y-2">
                    <p className="text-[13px] text-muted-foreground">{t('packages.nodeNvmNotInstalled')}</p>
                    <Button
                      size="sm"
                      className="rounded-xl"
                      onClick={onInstallNvm}
                      disabled={nvmInstalling}
                    >
                      {nvmInstalling ? (
                        <>
                          <Loader2 className="animate-spin h-3 w-3" />
                          {t('packages.installing')}
                        </>
                      ) : (
                        <>
                          <Download className="h-3 w-3" />
                          {t('packages.nodeInstallNvm')}
                        </>
                      )}
                    </Button>
                  </div>
                ) : nodeVersions.length === 0 ? (
                  <div className="space-y-2">
                    {nodeStatus?.installed && (
                      <div className="flex items-center justify-between rounded-xl border px-3 py-2">
                        <div className="flex items-center gap-2">
                          <span className="text-[13px] font-mono font-medium">{nodeStatus.version}</span>
                          <StatusPill tone="success">{t('packages.nodeActive')}</StatusPill>
                        </div>
                      </div>
                    )}
                    <p className="text-[12px] text-muted-foreground">{t('packages.nodeNoOtherVersions')}</p>
                  </div>
                ) : (
                  <div className="space-y-1.5">
                    {nodeVersions.map((v) => (
                      <div key={v.version} className="flex items-center justify-between rounded-xl border px-3 py-2">
                        <div className="flex items-center gap-2">
                          <span className="text-[13px] font-mono font-medium">{v.version}</span>
                          {v.active && (
                            <StatusPill tone="success">{t('packages.nodeActive')}</StatusPill>
                          )}
                          {v.lts && (
                            <StatusPill tone="primary">LTS</StatusPill>
                          )}
                        </div>
                        <div className="flex items-center gap-1">
                          {!v.active && (
                            <>
                              <Button
                                variant="ghost"
                                size="sm"
                                className="h-7 text-[12px] rounded-lg"
                                onClick={() => handleSwitch(v.version)}
                                disabled={switching !== null}
                              >
                                {switching === v.version ? (
                                  <Loader2 className="h-3 w-3 animate-spin" />
                                ) : (
                                  t('packages.nodeUse')
                                )}
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                                onClick={() => handleUninstallVersion(v.version)}
                                disabled={removing !== null}
                                aria-label={t('common.delete')}
                              >
                                {removing === v.version ? (
                                  <Loader2 className="h-3 w-3 animate-spin" />
                                ) : (
                                  <Trash2 className="h-3.5 w-3.5" />
                                )}
                              </Button>
                            </>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>

              {/* Available LTS versions to install */}
              {remoteLTS.length > 0 && (
                <div className="space-y-2">
                  <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('packages.nodeAvailableLTS')}</p>
                  <div className="space-y-1.5">
                    {remoteLTS
                      .filter((v) => !nodeVersions.some((nv) => nv.version === v))
                      .map((v) => (
                        <div key={v} className="flex items-center justify-between rounded-xl border border-dashed px-3 py-2">
                          <span className="text-[13px] font-mono text-muted-foreground">{v}</span>
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 text-[12px] rounded-lg"
                            onClick={() => handleInstallVersion(v)}
                            disabled={installingVersion !== null}
                          >
                            {installingVersion === v ? (
                              <Loader2 className="h-3 w-3 animate-spin" />
                            ) : (
                              <>
                                <Download className="h-3 w-3" />
                                {t('packages.install')}
                              </>
                            )}
                          </Button>
                        </div>
                      ))}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            {t('packages.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
