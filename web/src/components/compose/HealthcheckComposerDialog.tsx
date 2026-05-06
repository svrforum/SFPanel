import { useEffect, useState } from 'react'
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
import type { HealthcheckSpec, HealthcheckTestType } from '@/types/api'

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

async function sha256Hex(s: string): Promise<string> {
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
  const [spec, setSpec] = useState<HealthcheckSpec>(DEFAULTS)
  const [hasExisting, setHasExisting] = useState(false)
  const [replace, setReplace] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (!open) return
    queueMicrotask(() => {
      setSpec(DEFAULTS)
      setHasExisting(false)
      setReplace(false)
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
              const parts = m[1]
                .split(',')
                .map((p) => p.trim().replace(/^['"]|['"]$/g, ''))
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

  const validDurations =
    spec.test_type === 'NONE' ||
    (DURATION_RE.test(spec.interval) &&
      DURATION_RE.test(spec.timeout) &&
      DURATION_RE.test(spec.start_period))
  const validTest =
    spec.test_type === 'NONE' || spec.test_value.trim() !== ''
  const validReplace = !hasExisting || replace
  const canSubmit = validDurations && validTest && validReplace && spec.retries > 0

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const baseHash = await sha256Hex(baseYaml)
      const res = await api.applyHealthcheck(
        project,
        service,
        spec,
        replace || hasExisting,
        baseHash,
      )
      toast.success('Healthcheck inserted — review and Save & Deploy')
      onApplied(res.yaml)
      onOpenChange(false)
    } catch (err) {
      toast.error((err as Error).message || 'Healthcheck 적용 실패')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Healthcheck — {service}</DialogTitle>
          <DialogDescription>
            compose YAML의 services.{service}.healthcheck 블록을 추가/수정합니다. 자동
            배포되지 않습니다 — 미리보기 후 Save & Deploy 하세요.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-3">
          <div className="space-y-1.5">
            <Label>Test 명령어</Label>
            {(['CMD-SHELL', 'CMD', 'NONE'] as HealthcheckTestType[]).map((t) => (
              <label key={t} className="flex items-start gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="test_type"
                  className="mt-1"
                  checked={spec.test_type === t}
                  onChange={() => setSpec((s) => ({ ...s, test_type: t }))}
                />
                <span className="text-[13px]">
                  <strong>{t}</strong>
                  {t === 'CMD-SHELL' && ' — 셸에서 한 줄 실행'}
                  {t === 'CMD' && ' — 인자 배열 (| 로 구분)'}
                  {t === 'NONE' && ' — 이미지의 baked-in healthcheck 비활성'}
                </span>
              </label>
            ))}
          </div>
          {spec.test_type !== 'NONE' && (
            <div className="space-y-1.5">
              <Label htmlFor="hc-test-value">
                {spec.test_type === 'CMD-SHELL' ? '셸 명령어' : '인자 (| 구분)'}
              </Label>
              <Input
                id="hc-test-value"
                value={spec.test_value}
                onChange={(e) => setSpec((s) => ({ ...s, test_value: e.target.value }))}
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
                <Label htmlFor="hc-interval">주기 (interval)</Label>
                <Input
                  id="hc-interval"
                  value={spec.interval}
                  onChange={(e) => setSpec((s) => ({ ...s, interval: e.target.value }))}
                  placeholder="30s"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-timeout">타임아웃</Label>
                <Input
                  id="hc-timeout"
                  value={spec.timeout}
                  onChange={(e) => setSpec((s) => ({ ...s, timeout: e.target.value }))}
                  placeholder="10s"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-retries">재시도</Label>
                <Input
                  id="hc-retries"
                  type="number"
                  min={1}
                  value={spec.retries}
                  onChange={(e) =>
                    setSpec((s) => ({ ...s, retries: parseInt(e.target.value, 10) || 0 }))
                  }
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-start-period">Grace period</Label>
                <Input
                  id="hc-start-period"
                  value={spec.start_period}
                  onChange={(e) => setSpec((s) => ({ ...s, start_period: e.target.value }))}
                  placeholder="30s"
                />
              </div>
            </div>
          )}
          {hasExisting && (
            <label className="flex items-start gap-2 text-[12px] text-muted-foreground">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={replace}
                onChange={(e) => setReplace(e.target.checked)}
              />
              이 service에 이미 healthcheck가 있습니다 — 덮어쓰기
            </label>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              취소
            </Button>
            <Button type="submit" disabled={submitting || !canSubmit}>
              {submitting ? '적용 중…' : 'Compose YAML에 적용'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
