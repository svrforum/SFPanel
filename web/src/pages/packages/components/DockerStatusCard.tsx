import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertCircle, Check, CheckCircle2, Download, Loader2, Server, X } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { DockerStatus } from '@/types/api'
import { Button } from '@/components/ui/button'
import { streamErrorMessage, type SSEOutput } from '@/components/OutputDialog'

// Docker engine status + one-click install (SSE streamed into the shared
// output dialog; postTextStream keeps the cluster ?node= scope).
export function DockerStatusCard({ output }: { output: SSEOutput }) {
  const { t } = useTranslation()
  const [dockerStatus, setDockerStatus] = useState<DockerStatus | null>(null)
  const [checking, setChecking] = useState(false)
  const [installing, setInstalling] = useState(false)

  const fetchDockerStatus = useCallback(async () => {
    setChecking(true)
    try {
      const data = await api.getDockerStatus()
      setDockerStatus(data)
    } catch {
      toast.error(t('packages.dockerStatusFailed'))
    } finally {
      setChecking(false)
    }
  }, [t])

  useEffect(() => {
    fetchDockerStatus()
  }, [fetchDockerStatus])

  const handleInstallDocker = useCallback(async () => {
    setInstalling(true)
    output.openOutput(t('packages.installingDocker'))
    try {
      await output.runStream('/packages/install-docker')
      toast.success(t('packages.dockerInstallSuccess'))
      output.finishOutput()
      await fetchDockerStatus()
    } catch (err: unknown) {
      const message = streamErrorMessage(err, t('packages.streamStartFailed'))
      output.appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
      output.finishOutput()
      toast.error(message)
    } finally {
      setInstalling(false)
    }
  }, [output, fetchDockerStatus, t])

  return (
    <div className="bg-card rounded-2xl card-shadow">
      <div className="px-6 pt-5 pb-4">
        <h3 className="text-[15px] font-semibold flex items-center gap-2">
          <Server className="h-4 w-4" aria-hidden="true" />
          {t('packages.dockerStatus')}
        </h3>
        <p className="text-[13px] text-muted-foreground mt-1">
          {t('packages.dockerDescription')}
        </p>
      </div>
      <div className="px-6 pb-5">
        {checking ? (
          <div className="flex items-center gap-2 text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
            <span className="text-[13px]">{t('packages.checkingDocker')}</span>
          </div>
        ) : dockerStatus === null ? (
          <div className="flex items-center gap-2 text-muted-foreground">
            <AlertCircle className="h-4 w-4" aria-hidden="true" />
            <span className="text-[13px]">{t('packages.dockerStatusUnavailable')}</span>
          </div>
        ) : !dockerStatus.installed ? (
          <div className="space-y-4">
            <div className="flex items-center gap-2 text-destructive">
              <X className="h-5 w-5" aria-hidden="true" />
              <span className="text-[13px] font-medium">
                {t('packages.dockerNotInstalled')}
              </span>
            </div>
            <p className="text-[13px] text-muted-foreground">
              {t('packages.dockerNotInstalledHint')}
            </p>
            <Button
              size="lg"
              className="rounded-xl"
              onClick={handleInstallDocker}
              disabled={installing}
            >
              {installing ? (
                <>
                  <Loader2 className="animate-spin" aria-hidden="true" />
                  {t('packages.installingDocker')}
                </>
              ) : (
                <>
                  <Download aria-hidden="true" />
                  {t('packages.installDocker')}
                </>
              )}
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <div className="space-y-1">
              <p className="text-[11px] text-muted-foreground uppercase tracking-wider">
                {t('packages.dockerInstalled')}
              </p>
              <div className="flex items-center gap-1.5">
                <CheckCircle2 className="h-4 w-4 text-success" aria-hidden="true" />
                <span className="text-[13px] font-medium">{t('packages.yes')}</span>
              </div>
            </div>
            <div className="space-y-1">
              <p className="text-[11px] text-muted-foreground uppercase tracking-wider">
                {t('packages.dockerVersion')}
              </p>
              <p className="text-[13px] font-medium font-mono">
                {dockerStatus.version || t('packages.unknown')}
              </p>
            </div>
            <div className="space-y-1">
              <p className="text-[11px] text-muted-foreground uppercase tracking-wider">
                {t('packages.dockerRunning')}
              </p>
              <div className="flex items-center gap-1.5">
                {dockerStatus.running ? (
                  <>
                    <div className="h-2 w-2 rounded-full bg-success" />
                    <span className="text-[13px] font-medium">{t('packages.running')}</span>
                  </>
                ) : (
                  <>
                    <div className="h-2 w-2 rounded-full bg-destructive" />
                    <span className="text-[13px] font-medium">{t('packages.stopped')}</span>
                  </>
                )}
              </div>
            </div>
            <div className="space-y-1">
              <p className="text-[11px] text-muted-foreground uppercase tracking-wider">
                {t('packages.dockerCompose')}
              </p>
              <div className="flex items-center gap-1.5">
                {dockerStatus.compose_available ? (
                  <>
                    <Check className="h-4 w-4 text-success" aria-hidden="true" />
                    <span className="text-[13px] font-medium">{t('packages.available')}</span>
                  </>
                ) : (
                  <>
                    <X className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
                    <span className="text-[13px] font-medium">{t('packages.notAvailable')}</span>
                  </>
                )}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
