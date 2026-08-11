import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUp, CheckCircle2, Eye, FileCode, FileText, Loader2, Save, XCircle } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import ComposeEditor from '@/components/compose/ComposeEditor'

/**
 * Compose/.env editor tab panel for a stack. The buffers (yaml/env) and the
 * active sub-tab stay lifted in DockerStacks — DiffSheet reads the yaml buffer
 * and the healthcheck composer rewrites it (and force-switches the sub-tab) —
 * but the save/validate machinery and its state live here.
 */
export function StackEditorPanel({
  project,
  composeFileName,
  yaml,
  onYamlChange,
  env,
  onEnvChange,
  tab,
  onTabChange,
  deploying,
  onDeploy,
  onOpenDiff,
  onEnvSaved,
}: {
  project: string
  composeFileName: string
  yaml: string
  onYamlChange: (value: string) => void
  env: string
  onEnvChange: (value: string) => void
  tab: 'compose' | 'env'
  onTabChange: (tab: 'compose' | 'env') => void
  /** True while the page-level deploy flow (save + up-stream) is running. */
  deploying: boolean
  onDeploy: () => void
  onOpenDiff: () => void
  /** Fired after a successful .env save so the caller can refresh has_env. */
  onEnvSaved: () => void
}) {
  const { t } = useTranslation()
  const [saving, setSaving] = useState(false)
  const [envSaving, setEnvSaving] = useState(false)
  const [validating, setValidating] = useState(false)
  const [validationResult, setValidationResult] = useState<{ valid: boolean; message: string } | null>(null)

  useEffect(() => {
    setValidationResult(null)
  }, [yaml])

  const handleValidate = async () => {
    setValidating(true)
    setValidationResult(null)
    try {
      const result = await api.validateCompose(project)
      setValidationResult(result)
      if (result.valid) {
        toast.success(t('docker.stacks.validateSuccess'))
      } else {
        toast.error(t('docker.stacks.validateFailed'))
      }
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.validateFailed'))
    } finally {
      setValidating(false)
    }
  }

  const handleSaveYaml = async () => {
    if (!yaml.trim()) return
    setSaving(true)
    try {
      await api.updateComposeProject(project, yaml)
      toast.success(t('docker.stacks.saved'))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  const handleSaveEnv = async () => {
    setEnvSaving(true)
    try {
      await api.updateComposeEnv(project, env)
      toast.success(t('docker.stacks.envSaved'))
      // Refresh project to update has_env status
      onEnvSaved()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.envSaveFailed'))
    } finally {
      setEnvSaving(false)
    }
  }

  return (
    <div className="space-y-3">
      {/* Compose / Env sub-tabs */}
      <div className="flex items-center gap-1 bg-secondary/40 rounded-xl p-1 w-fit overflow-x-auto">
        <button
          className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition-all outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
            tab === 'compose' ? 'bg-primary/10 text-primary card-shadow' : 'text-muted-foreground hover:text-foreground'
          }`}
          onClick={() => onTabChange('compose')}
        >
          <FileCode className={`h-3.5 w-3.5 ${tab === 'compose' ? 'text-primary' : ''}`} />
          {composeFileName}
        </button>
        <button
          className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition-all outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
            tab === 'env' ? 'bg-warning/10 text-warning card-shadow' : 'text-muted-foreground hover:text-foreground'
          }`}
          onClick={() => onTabChange('env')}
        >
          <FileText className={`h-3.5 w-3.5 ${tab === 'env' ? 'text-warning' : ''}`} />
          .env
        </button>
      </div>

      {tab === 'compose' ? (
        <>
          <div className="rounded-2xl overflow-hidden border-t-2 border-t-primary card-shadow">
            <ComposeEditor value={yaml} onChange={onYamlChange} />
          </div>
          {validationResult && (
            <div className={`flex items-center gap-2 px-3 py-2 rounded-xl text-[13px] ${
              validationResult.valid
                ? 'bg-success/10 text-success'
                : 'bg-destructive/10 text-destructive'
            }`}>
              {validationResult.valid ? <CheckCircle2 className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
              <span>{validationResult.valid ? t('docker.stacks.configValid') : validationResult.message}</span>
            </div>
          )}
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              variant="outline"
              onClick={handleValidate}
              disabled={validating || !yaml.trim()}
              className="rounded-xl"
            >
              {validating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <CheckCircle2 className="h-3.5 w-3.5" />}
              {t('docker.stacks.validate')}
            </Button>
            <Button
              variant="outline"
              onClick={onOpenDiff}
              disabled={!yaml.trim()}
              className="rounded-xl"
              title={!yaml.trim() ? t('compose.diff.yamlRequired', 'Enter YAML first') : t('compose.diff.title', 'Preview changes')}
            >
              <Eye className="h-3.5 w-3.5" />
              {t('compose.diff.title', 'Preview changes')}
            </Button>
            <Button
              variant="outline"
              onClick={handleSaveYaml}
              disabled={saving || deploying || !yaml.trim()}
              className="rounded-xl"
            >
              <Save className="h-3.5 w-3.5" />
              {saving ? t('common.saving') : t('common.save')}
            </Button>
            <Button
              onClick={onDeploy}
              disabled={saving || deploying || !yaml.trim()}
              className="rounded-xl bg-success hover:bg-success/90"
            >
              <ArrowUp className="h-3.5 w-3.5" />
              {deploying ? t('common.saving') : t('docker.stacks.deploy')}
            </Button>
          </div>
        </>
      ) : (
        <>
          <div className="rounded-2xl overflow-hidden border-t-2 border-t-warning card-shadow">
            <ComposeEditor value={env} onChange={onEnvChange} language="ini" />
          </div>
          <div className="flex justify-end gap-2">
            <Button
              onClick={handleSaveEnv}
              disabled={envSaving}
              className="rounded-xl"
            >
              <Save className="h-3.5 w-3.5" />
              {envSaving ? t('common.saving') : t('common.save')}
            </Button>
          </div>
        </>
      )}
    </div>
  )
}
