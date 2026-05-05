import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Pencil, Trash2, Plus } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { api } from '@/lib/api'
import { RuleEditDialog } from '@/components/security/RuleEditDialog'
import type {
  SecurityPolicy,
  SecurityRule,
  SecurityMode,
  CosignStatus,
} from '@/types/api'

const MODES: { value: SecurityMode; label: string; desc: string }[] = [
  { value: 'off', label: '끔 (off)', desc: '검증 비활성. 기본값.' },
  { value: 'warn', label: '경고만 (warn)', desc: '미서명/실패 이미지도 통과. 결과만 기록.' },
  { value: 'require', label: '강제 (require)', desc: '미서명/실패 이미지는 pull 차단.' },
]

export default function ImageSignatureSettings() {
  const [policy, setPolicy] = useState<SecurityPolicy>({ mode: 'off', rules: [] })
  const [cosign, setCosign] = useState<CosignStatus | null>(null)
  const [editIdx, setEditIdx] = useState<number | null>(null)
  const [editOpen, setEditOpen] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api.getSecurityPolicy().then((p) => setPolicy({ mode: p.mode, rules: p.rules ?? [] })).catch(() => {})
    api.getCosignStatus().then(setCosign).catch(() => {})
  }, [])

  async function persist(next: SecurityPolicy) {
    setSaving(true)
    try {
      await api.updateSecurityPolicy(next)
      setPolicy(next)
      toast.success('보안 정책 저장됨')
    } catch (e) {
      toast.error((e as Error).message || '저장 실패')
    } finally {
      setSaving(false)
    }
  }

  function onSaveRule(rule: SecurityRule) {
    const next = { ...policy }
    if (editIdx !== null && editIdx < policy.rules.length) {
      next.rules = [...policy.rules]
      next.rules[editIdx] = rule
    } else {
      next.rules = [...policy.rules, rule]
    }
    void persist(next)
  }

  function onDeleteRule(idx: number) {
    if (!confirm('이 룰을 삭제하시겠습니까?')) return
    const next = { ...policy, rules: policy.rules.filter((_, i) => i !== idx) }
    void persist(next)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-[15px]">이미지 서명 검증</CardTitle>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="space-y-2">
          <Label className="text-[13px]">정책</Label>
          {MODES.map((m) => (
            <label key={m.value} className="flex items-start gap-2 cursor-pointer">
              <input
                type="radio"
                name="mode"
                className="mt-1"
                checked={policy.mode === m.value}
                disabled={saving}
                onChange={() => persist({ ...policy, mode: m.value })}
              />
              <div>
                <div className="text-[13px] font-medium">{m.label}</div>
                <div className="text-[11px] text-muted-foreground">{m.desc}</div>
              </div>
            </label>
          ))}
        </div>

        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <Label className="text-[13px]">허용 목록</Label>
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                setEditIdx(null)
                setEditOpen(true)
              }}
            >
              <Plus className="h-3.5 w-3.5 mr-1" />
              룰 추가
            </Button>
          </div>
          {policy.rules.length === 0 ? (
            <div className="text-[12px] text-muted-foreground py-3 text-center border rounded-md">
              아직 룰이 없습니다.
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>패턴</TableHead>
                  <TableHead>Subject prefix</TableHead>
                  <TableHead>Issuer</TableHead>
                  <TableHead className="w-20"></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {policy.rules.map((r, i) => (
                  <TableRow key={i}>
                    <TableCell className="font-mono text-[12px]">{r.pattern}</TableCell>
                    <TableCell
                      className="font-mono text-[11px] truncate max-w-[260px]"
                      title={r.identity.subject_prefix}
                    >
                      {r.identity.subject_prefix}
                    </TableCell>
                    <TableCell
                      className="text-[11px] truncate max-w-[160px]"
                      title={r.identity.issuer}
                    >
                      {r.identity.issuer}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => {
                          setEditIdx(i)
                          setEditOpen(true)
                        }}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => onDeleteRule(i)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>

        <div className="space-y-1 pt-3 border-t">
          <Label className="text-[13px]">cosign 바이너리</Label>
          {cosign?.installed ? (
            <div className="text-[12px] text-muted-foreground font-mono">
              ✓ {cosign.path}
              <br />
              {cosign.version.split('\n')[0]}
            </div>
          ) : (
            <div className="text-[12px] text-muted-foreground">
              ⏳ 미설치 — 정책을 warn 또는 require로 활성화하면 첫 검증 시 자동 설치됩니다.
            </div>
          )}
        </div>
      </CardContent>

      <RuleEditDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        initial={editIdx !== null ? policy.rules[editIdx] : undefined}
        onSave={onSaveRule}
      />
    </Card>
  )
}
