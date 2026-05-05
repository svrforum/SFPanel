import { useEffect, useState, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { MoreVertical, Trash2, Pencil, Download } from 'lucide-react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { api } from '@/lib/api'
import type { Fork } from '@/types/api'

interface Props {
  search?: string
  category?: string
}

export function ForkList({ search = '', category = '' }: Props) {
  const [forks, setForks] = useState<Fork[]>([])
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await api.listForks()
      setForks(data ?? [])
    } catch {
      setForks([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function onDelete(id: string, name: string) {
    if (!confirm(`'${name}' 템플릿을 삭제하시겠습니까?`)) return
    try {
      await api.deleteFork(id)
      toast.success(`'${name}' 삭제됨`)
      void load()
    } catch (err) {
      toast.error((err as Error).message || '삭제 실패')
    }
  }

  const filtered = forks.filter((f) => {
    if (category && f.category !== category) return false
    if (search) {
      const q = search.toLowerCase()
      return (
        f.name.toLowerCase().includes(q) || f.description.toLowerCase().includes(q)
      )
    }
    return true
  })

  if (loading) {
    return (
      <div className="text-muted-foreground text-[13px] py-8 text-center">
        불러오는 중…
      </div>
    )
  }
  if (forks.length === 0) {
    return (
      <div className="text-muted-foreground text-[13px] py-12 text-center">
        아직 저장된 Template이 없습니다. Docker Stack 상세에서 "Template으로 저장"으로 만들어보세요.
      </div>
    )
  }

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
      {filtered.map((f) => (
        <Card key={f.id} className="flex flex-col">
          <CardHeader className="pb-2">
            <div className="flex items-start justify-between gap-2">
              <CardTitle className="text-[14px] truncate flex-1">📦 {f.name}</CardTitle>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="ghost" size="icon-xs" aria-label="more">
                    <MoreVertical className="h-4 w-4" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem asChild>
                    <Link to={`/appstore/forks/${f.id}`}>
                      <Pencil className="h-3.5 w-3.5 mr-1.5" />
                      편집
                    </Link>
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onClick={() => onDelete(f.id, f.name)}
                    className="text-destructive"
                  >
                    <Trash2 className="h-3.5 w-3.5 mr-1.5" />
                    삭제
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
            <div className="text-[11px] text-muted-foreground">{f.category}</div>
          </CardHeader>
          <CardContent className="flex-1 flex flex-col">
            <p className="text-[12px] text-muted-foreground line-clamp-2 mb-3 min-h-[32px]">
              {f.description || ' '}
            </p>
            <Button asChild size="sm" className="mt-auto">
              <Link to={`/appstore/${f.id}`}>
                <Download className="h-3.5 w-3.5 mr-1" />
                설치
              </Link>
            </Button>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
