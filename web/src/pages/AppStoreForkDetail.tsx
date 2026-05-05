import { useEffect, useState } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { ArrowLeft, Save, Trash2, Download } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import type { Fork } from '@/types/api'

export default function AppStoreForkDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [fork, setFork] = useState<Fork | null>(null)
  const [loading, setLoading] = useState(true)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [category, setCategory] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!id) return
    let cancelled = false
    api
      .getFork(id)
      .then((f) => {
        if (cancelled) return
        setFork(f)
        setName(f.name)
        setDescription(f.description)
        setCategory(f.category)
      })
      .catch(() => {
        if (!cancelled) toast.error('Template을 불러올 수 없습니다.')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [id])

  async function onSave() {
    if (!id) return
    setSaving(true)
    try {
      await api.updateFork(id, { name: name.trim(), description, category })
      toast.success('수정됨')
      const refreshed = await api.getFork(id)
      setFork(refreshed)
    } catch (err) {
      toast.error((err as Error).message || '저장 실패')
    } finally {
      setSaving(false)
    }
  }

  async function onDelete() {
    if (!id || !fork) return
    if (!confirm(`'${fork.name}' 템플릿을 삭제하시겠습니까?`)) return
    try {
      await api.deleteFork(id)
      toast.success('삭제됨')
      navigate('/appstore')
    } catch (err) {
      toast.error((err as Error).message || '삭제 실패')
    }
  }

  if (loading) {
    return <div className="p-6 text-muted-foreground text-[13px]">불러오는 중…</div>
  }
  if (!fork) {
    return (
      <div className="p-6 text-muted-foreground text-[13px]">
        Template을 찾을 수 없습니다.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" onClick={() => navigate('/appstore')}>
          <ArrowLeft className="h-3.5 w-3.5 mr-1" />
          뒤로
        </Button>
        <h1 className="text-[20px] font-bold tracking-tight">{fork.name}</h1>
        <span className="text-[12px] text-muted-foreground font-mono">{fork.id}</span>
      </div>

      <div className="grid gap-4 max-w-xl">
        <div className="space-y-1.5">
          <Label htmlFor="f-name">이름</Label>
          <Input
            id="f-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={100}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="f-desc">설명</Label>
          <Textarea
            id="f-desc"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={3}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="f-cat">카테고리</Label>
          <Input id="f-cat" value={category} onChange={(e) => setCategory(e.target.value)} />
        </div>
      </div>

      <div className="flex items-center gap-2">
        <Button onClick={onSave} disabled={saving || !name.trim()}>
          <Save className="h-3.5 w-3.5 mr-1" />
          {saving ? '저장 중…' : '저장'}
        </Button>
        <Button asChild variant="outline">
          <Link to={`/appstore?app=${encodeURIComponent(fork.id)}`}>
            <Download className="h-3.5 w-3.5 mr-1" />
            설치
          </Link>
        </Button>
        <Button onClick={onDelete} variant="destructive">
          <Trash2 className="h-3.5 w-3.5 mr-1" />
          삭제
        </Button>
      </div>

      <div>
        <Label>compose YAML (immutable)</Label>
        <pre className="mt-1 p-3 bg-secondary/50 rounded-md text-[11px] font-mono overflow-auto max-h-[400px]">
          {fork.compose}
        </pre>
      </div>
    </div>
  )
}
