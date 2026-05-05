import { useState } from 'react'
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
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  stackName: string
  onSuccess?: (forkId: string) => void
}

export function ForkCreateDialog({ open, onOpenChange, stackName, onSuccess }: Props) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [category, setCategory] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setSubmitting(true)
    try {
      const res = await api.createFork({
        stack_name: stackName,
        name: name.trim(),
        description: description.trim(),
        category: category.trim(),
      })
      toast.success(`'${name}' 템플릿이 저장되었습니다`)
      onOpenChange(false)
      setName('')
      setDescription('')
      setCategory('')
      onSuccess?.(res.id)
    } catch (err) {
      toast.error((err as Error).message || '저장 실패')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Template으로 저장</DialogTitle>
          <DialogDescription>
            현재 stack의 compose YAML과 환경 변수가 자동으로 포함됩니다.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="fork-name">이름 *</Label>
            <Input
              id="fork-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-template"
              maxLength={100}
              required
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="fork-desc">설명</Label>
            <Textarea
              id="fork-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={2}
              placeholder="짧은 한 줄 설명…"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="fork-cat">카테고리</Label>
            <Input
              id="fork-cat"
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              placeholder="내 Templates"
            />
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              취소
            </Button>
            <Button type="submit" disabled={submitting || !name.trim()}>
              {submitting ? '저장 중…' : '저장'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
