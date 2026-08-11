import { useEffect, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import type { HealthcheckSpec, HealthcheckTestType, HealthcheckTestResult } from '@/types/api'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  project: string
  service: string
  baseYaml: string
  onApplied: (newYaml: string) => void
}

const DEFAULTS: HealthcheckSpec = {
  test_type: 'CMD-SHELL',
  test_value: '',
  interval: '30s',
  timeout: '10s',
  retries: 3,
  start_period: '30s',
}

const DURATION_RE = /^\d+(\.\d+)?(ns|us|µs|ms|s|m|h)([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))*$/

interface Preset {
  label: string
  test_type: HealthcheckTestType
  test_value: string
}

const PRESETS: Preset[] = [
  { label: 'Custom', test_type: 'CMD-SHELL', test_value: '' },
  { label: 'HTTP GET /health', test_type: 'CMD-SHELL', test_value: 'curl -f http://localhost:PORT/health || exit 1' },
  { label: 'PostgreSQL (pg_isready)', test_type: 'CMD', test_value: 'pg_isready|-U|postgres' },
  { label: 'Redis (PING)', test_type: 'CMD-SHELL', test_value: 'redis-cli ping | grep PONG' },
  { label: 'MySQL (ping)', test_type: 'CMD-SHELL', test_value: 'mysqladmin ping -h localhost || exit 1' },
]

async function sha256Hex(s: string): Promise<string> {
  // crypto.subtle is only available in secure contexts (HTTPS or localhost).
  // SFPanel is commonly accessed over plain HTTP on a LAN, so we degrade
  // gracefully: empty hash → backend skips the concurrent-edit precondition.
  // Trade-off: stability guarantee #4 is best-effort in non-secure contexts.
  if (typeof crypto === 'undefined' || !crypto.subtle) return ''
  const buf = new TextEncoder().encode(s)
  const hash = await crypto.subtle.digest('SHA-256', buf)
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}

export function HealthcheckComposerDialog({
  open,
  onOpenChange,
  project,
  service,
  baseYaml,
  onApplied,
}: Props) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [spec, setSpec] = useState<HealthcheckSpec>(DEFAULTS)
  const [hasExisting, setHasExisting] = useState(false)
  const [replace, setReplace] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<HealthcheckTestResult | null>(null)

  useEffect(() => {
    if (!open) return
    queueMicrotask(() => {
      setSpec(DEFAULTS)
      setHasExisting(false)
      setReplace(false)
      setTestResult(null)
      // Cheap client-side detection so the dialog can pre-populate from
      // an existing healthcheck without a round-trip. Backend
      // ParseHealthcheck is the source of truth on submit.
      const lines = baseYaml.split('\n')
      let inService = false
      let svcIndent = -1
      let inHealth = false
      const next: Partial<HealthcheckSpec> = {}
      for (const line of lines) {
        const indent = line.match(/^( *)/)?.[1].length ?? 0
        const trimmed = line.trim()
        if (!inService && trimmed === `${service}:`) {
          inService = true
          svcIndent = indent
          continue
        }
        if (inService && indent <= svcIndent && trimmed !== '') break
        if (!inService) continue
        if (trimmed.startsWith('healthcheck:')) {
          inHealth = true
          setHasExisting(true)
          continue
        }
        if (inHealth) {
          if (indent <= svcIndent + 2 && trimmed !== '') {
            inHealth = false
            continue
          }
          if (trimmed.startsWith('test:')) {
            const m = trimmed.match(/test:\s*\[(.*)\]/)
            if (m) {
              const parts = m[1].split(',').map((p) => p.trim().replace(/^['"]|['"]$/g, ''))
              if (parts[0] === 'NONE') {
                next.test_type = 'NONE'
              } else if (parts[0] === 'CMD-SHELL') {
                next.test_type = 'CMD-SHELL'
                next.test_value = parts[1] ?? ''
              } else if (parts[0] === 'CMD') {
                next.test_type = 'CMD'
                next.test_value = parts.slice(1).join('|')
              }
            }
          } else if (trimmed.startsWith('interval:')) {
            next.interval = trimmed.slice(9).trim()
          } else if (trimmed.startsWith('timeout:')) {
            next.timeout = trimmed.slice(8).trim()
          } else if (trimmed.startsWith('retries:')) {
            next.retries = parseInt(trimmed.slice(8).trim(), 10) || 3
          } else if (trimmed.startsWith('start_period:')) {
            next.start_period = trimmed.slice(13).trim()
          }
        }
      }
      if (Object.keys(next).length > 0) {
        setSpec({ ...DEFAULTS, ...next })
      }
    })
  }, [open, baseYaml, service])

  function applyPreset(p: Preset) {
    if (p.label === 'Custom') return
    setSpec((s) => ({ ...s, test_type: p.test_type, test_value: p.test_value }))
    setTestResult(null)
  }

  const validDurations =
    spec.test_type === 'NONE' ||
    (DURATION_RE.test(spec.interval) && DURATION_RE.test(spec.timeout) && DURATION_RE.test(spec.start_period))
  const validTest = spec.test_type === 'NONE' || spec.test_value.trim() !== ''
  const validReplace = !hasExisting || replace
  const canSubmit = validDurations && validTest && validReplace && spec.retries > 0
  const canTest = spec.test_type !== 'NONE' && spec.test_value.trim() !== '' && !testing && !submitting

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const baseHash = await sha256Hex(baseYaml)
      const res = await api.applyHealthcheck(project, service, spec, replace || hasExisting, baseHash)
      toast.success(t('compose.healthcheck.appliedToast', 'Healthcheck applied to the compose YAML — use Deploy to apply it to the container'))
      onApplied(res.yaml)
      onOpenChange(false)
    } catch (err) {
      toast.error((err as Error).message || t('compose.healthcheck.applyFailed', 'Failed to apply healthcheck'))
    } finally {
      setSubmitting(false)
    }
  }

  async function onTestNow() {
    if (!canTest) return
    setTesting(true)
    setTestResult(null)
    try {
      const res = await api.testHealthcheck(project, service, spec)
      setTestResult(res)
    } catch (err) {
      toast.error((err as Error).message || t('compose.healthcheck.testFailed', 'Test failed'))
    } finally {
      setTesting(false)
    }
  }

  async function onRemove() {
    if (!hasExisting) return
    if (!(await confirm({ title: t('compose.healthcheck.removeConfirm', { service }), danger: true }))) return
    setRemoving(true)
    try {
      const baseHash = await sha256Hex(baseYaml)
      const res = await api.removeHealthcheck(project, service, baseHash)
      toast.success(t('compose.healthcheck.removedToast', 'Healthcheck removed from the compose YAML — use Deploy to apply it to the container'))
      onApplied(res.yaml)
      onOpenChange(false)
    } catch (err) {
      toast.error((err as Error).message || t('compose.healthcheck.removeFailed', 'Failed to remove healthcheck'))
    } finally {
      setRemoving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="truncate">{t('compose.healthcheck.title', 'Healthcheck')} — {service}</DialogTitle>
          <DialogDescription>
            {t('compose.healthcheck.description', 'Adds or updates the services.{{service}}.healthcheck block in the compose YAML. Nothing is deployed automatically — preview, then Save & Deploy.', { service })}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="hc-preset">{t('compose.healthcheck.preset', 'Preset')}</Label>
            <select
              id="hc-preset"
              className="w-full h-9 border rounded-md px-2 text-[13px] bg-transparent outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
              defaultValue="Custom"
              onChange={(e) => {
                const p = PRESETS.find((x) => x.label === e.target.value)
                if (p) applyPreset(p)
              }}
            >
              {PRESETS.map((p) => (
                <option key={p.label}>{p.label}</option>
              ))}
            </select>
            <p className="text-[11px] text-muted-foreground">
              <Trans
                i18nKey="compose.healthcheck.presetHint"
                defaults="Selecting a preset fills in the command. Edit placeholders like <code>PORT</code> yourself."
                components={{ code: <code /> }}
              />
            </p>
          </div>

          <div className="space-y-1.5">
            <Label>{t('compose.healthcheck.testCommand', 'Test command')}</Label>
            {(['CMD-SHELL', 'CMD', 'NONE'] as HealthcheckTestType[]).map((type) => (
              <label key={type} className="flex items-start gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="test_type"
                  className="mt-1 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                  checked={spec.test_type === type}
                  onChange={() => setSpec((s) => ({ ...s, test_type: type }))}
                />
                <span className="text-[13px]">
                  <strong>{type}</strong>
                  {type === 'CMD-SHELL' && ` — ${t('compose.healthcheck.typeShell', 'run one line in a shell')}`}
                  {type === 'CMD' && ` — ${t('compose.healthcheck.typeCmd', 'argument array (separated by |)')}`}
                  {type === 'NONE' && ` — ${t('compose.healthcheck.typeNone', "disable the image's baked-in healthcheck")}`}
                </span>
              </label>
            ))}
          </div>

          {spec.test_type !== 'NONE' && (
            <div className="space-y-1.5">
              <Label htmlFor="hc-test-value">{spec.test_type === 'CMD-SHELL' ? t('compose.healthcheck.shellCommand', 'Shell command') : t('compose.healthcheck.cmdArgs', 'Arguments (| separated)')}</Label>
              <Input
                id="hc-test-value"
                value={spec.test_value}
                onChange={(e) => {
                  setSpec((s) => ({ ...s, test_value: e.target.value }))
                  setTestResult(null)
                }}
                placeholder={
                  spec.test_type === 'CMD-SHELL'
                    ? 'curl -f http://localhost:8096/health || exit 1'
                    : 'curl|-f|http://localhost:8096/health'
                }
                required
              />
            </div>
          )}

          {spec.test_type !== 'NONE' && (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="hc-interval">{t('compose.healthcheck.interval', 'Interval')}</Label>
                <Input id="hc-interval" value={spec.interval} onChange={(e) => setSpec((s) => ({ ...s, interval: e.target.value }))} placeholder="30s" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-timeout">{t('compose.healthcheck.timeout', 'Timeout')}</Label>
                <Input id="hc-timeout" value={spec.timeout} onChange={(e) => setSpec((s) => ({ ...s, timeout: e.target.value }))} placeholder="10s" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-retries">{t('compose.healthcheck.retries', 'Retries')}</Label>
                <Input
                  id="hc-retries"
                  type="number"
                  min={1}
                  value={spec.retries}
                  onChange={(e) => setSpec((s) => ({ ...s, retries: parseInt(e.target.value, 10) || 0 }))}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-start-period">{t('compose.healthcheck.startPeriod', 'Grace period')}</Label>
                <Input
                  id="hc-start-period"
                  value={spec.start_period}
                  onChange={(e) => setSpec((s) => ({ ...s, start_period: e.target.value }))}
                  placeholder="30s"
                />
              </div>
            </div>
          )}

          {spec.test_type !== 'NONE' && (
            <div className="space-y-1.5 pt-1 border-t">
              <div className="flex items-center justify-between">
                <Label className="text-[12px]">{t('compose.healthcheck.testHint', 'Validate against the running container')}</Label>
                <Button type="button" size="sm" variant="outline" onClick={onTestNow} disabled={!canTest}>
                  {testing ? t('compose.healthcheck.testing', 'Running…') : t('compose.healthcheck.testNow', 'Test now')}
                </Button>
              </div>
              {testResult && (
                <div
                  className={`text-[12px] font-mono rounded-md p-2 ${
                    testResult.exit_code === 0 ? 'bg-success/10 text-success' : 'bg-destructive/10 text-destructive'
                  }`}
                >
                  <div>
                    {testResult.exit_code === 0 ? '✓' : '✗'} exit {testResult.exit_code} ({testResult.duration_ms}ms)
                  </div>
                  {testResult.stdout && <div className="mt-1 text-foreground/70">stdout: {testResult.stdout.split('\n')[0]}</div>}
                  {testResult.stderr && <div className="mt-1 text-foreground/70">stderr: {testResult.stderr.split('\n')[0]}</div>}
                </div>
              )}
            </div>
          )}

          {hasExisting && (
            <label className="flex items-start gap-2 text-[12px] text-muted-foreground">
              <input type="checkbox" className="mt-0.5 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
              {t('compose.healthcheck.overwriteExisting', 'This service already has a healthcheck — overwrite')}
            </label>
          )}

          <DialogFooter className="flex !justify-between">
            {hasExisting ? (
              <Button type="button" variant="ghost" className="text-destructive hover:bg-destructive/10" onClick={onRemove} disabled={removing || submitting}>
                {removing ? t('compose.healthcheck.removing', 'Removing…') : t('compose.healthcheck.remove', 'Remove healthcheck')}
              </Button>
            ) : <span />}
            <div className="flex items-center gap-2">
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" disabled={submitting || !canSubmit}>
                {submitting ? t('compose.healthcheck.applying', 'Applying…') : t('compose.healthcheck.apply', 'Apply to compose YAML')}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
