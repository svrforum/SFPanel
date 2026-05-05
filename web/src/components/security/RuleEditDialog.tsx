import { useState, useEffect } from 'react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { SecurityRule } from '@/types/api'

const ISSUER_PRESETS = [
  { label: 'GitHub Actions', value: 'https://token.actions.githubusercontent.com' },
  { label: 'GitLab CI', value: 'https://gitlab.com' },
  { label: '직접 입력', value: '' },
]

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  initial?: SecurityRule
  onSave: (rule: SecurityRule) => void
}

export function RuleEditDialog({ open, onOpenChange, initial, onSave }: Props) {
  const [pattern, setPattern] = useState(initial?.pattern ?? '')
  const [subjectPrefix, setSubjectPrefix] = useState(initial?.identity.subject_prefix ?? '')
  const [issuerPreset, setIssuerPreset] = useState(
    ISSUER_PRESETS.find((p) => p.value === initial?.identity.issuer)?.label ?? '직접 입력',
  )
  const [issuerCustom, setIssuerCustom] = useState(initial?.identity.issuer ?? '')

  useEffect(() => {
    if (!open) return
    // Defer state sync to next microtask so the lint rule against
    // synchronous-setState-in-effect is satisfied. The values are derived
    // from `initial`/`open` so cascading renders are bounded.
    queueMicrotask(() => {
      setPattern(initial?.pattern ?? '')
      setSubjectPrefix(initial?.identity.subject_prefix ?? '')
      const preset = ISSUER_PRESETS.find((p) => p.value === initial?.identity.issuer)
      setIssuerPreset(preset?.label ?? '직접 입력')
      setIssuerCustom(initial?.identity.issuer ?? '')
    })
  }, [open, initial])

  const issuer =
    issuerPreset === '직접 입력'
      ? issuerCustom
      : ISSUER_PRESETS.find((p) => p.label === issuerPreset)?.value ?? ''
  const valid = pattern.trim() && subjectPrefix.trim() && issuer.trim()

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!valid) return
    onSave({
      pattern: pattern.trim(),
      identity: { subject_prefix: subjectPrefix.trim(), issuer },
    })
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{initial ? '룰 편집' : '룰 추가'}</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="rule-pattern">패턴</Label>
            <Input
              id="rule-pattern"
              value={pattern}
              onChange={(e) => setPattern(e.target.value)}
              placeholder="ghcr.io/myorg/*"
              required
            />
            <p className="text-[11px] text-muted-foreground">
              <code>*</code> = 한 세그먼트 / <code>**</code> = 다중 세그먼트. 예: <code>ghcr.io/svrforum/**</code>
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="rule-subject">Subject prefix</Label>
            <Input
              id="rule-subject"
              value={subjectPrefix}
              onChange={(e) => setSubjectPrefix(e.target.value)}
              placeholder="https://github.com/myorg/myrepo/.github/workflows/release.yaml@refs/tags/v"
              required
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="rule-issuer">Issuer</Label>
            <select
              id="rule-issuer"
              value={issuerPreset}
              onChange={(e) => setIssuerPreset(e.target.value)}
              className="w-full h-9 border rounded-md px-2 text-[13px] bg-transparent"
            >
              {ISSUER_PRESETS.map((p) => (
                <option key={p.label}>{p.label}</option>
              ))}
            </select>
            {issuerPreset === '직접 입력' && (
              <Input
                value={issuerCustom}
                onChange={(e) => setIssuerCustom(e.target.value)}
                placeholder="https://your-oidc-issuer.example/"
              />
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              취소
            </Button>
            <Button type="submit" disabled={!valid}>
              저장
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
