import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Eye, EyeOff } from 'lucide-react'
import { api } from '@/lib/api'
import { toast } from 'sonner'

const GITHUB_URL_RE = /^https:\/\/github\.com\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+(\.git)?$/
const STACK_NAME_RE = /^[a-z0-9][a-z0-9-]{0,49}$/

interface Props {
  onSuccess: (projectName: string) => void
  onCancel: () => void
}

export function GitImportForm({ onSuccess, onCancel }: Props) {
  const [url, setUrl] = useState('')
  const [branch, setBranch] = useState('main')
  const [path, setPath] = useState('docker-compose.yml')
  const [token, setToken] = useState('')
  const [name, setName] = useState('')
  const [tokenVisible, setTokenVisible] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})

  function validate(): boolean {
    const e: Record<string, string> = {}
    if (!url) e.url = 'URL을 입력해주세요'
    else if (!GITHUB_URL_RE.test(url)) e.url = 'GitHub HTTPS URL만 지원합니다'
    if (!name) e.name = '스택 이름을 입력해주세요'
    else if (!STACK_NAME_RE.test(name)) e.name = '소문자/숫자/하이픈만, 1-50자'
    setErrors(e)
    return Object.keys(e).length === 0
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return
    setSubmitting(true)
    try {
      const res = await api.importFromGit({ url, branch, path, token: token || undefined, name })
      toast.success(`'${res.project_name}' 스택을 가져왔습니다`)
      onSuccess(res.project_name)
    } catch (err) {
      // The api client throws plain Error(message); the backend's messages
      // are already user-facing Korean (per Task 9 handler error mapping).
      // Show all errors as a form-bottom banner.
      const msg = (err as Error).message || '가져오기 실패'
      setErrors({ _form: msg })
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={onSubmit} className="space-y-3 text-[13px]">
      <div>
        <Label htmlFor="git-url">GitHub repo URL *</Label>
        <Input
          id="git-url"
          value={url}
          onChange={e => setUrl(e.target.value)}
          placeholder="https://github.com/user/repo.git"
          autoComplete="off"
        />
        {errors.url
          ? <p className="text-[12px] text-destructive mt-1">{errors.url}</p>
          : <p className="text-[12px] text-muted-foreground mt-1">GitHub HTTPS URL만 지원</p>}
      </div>

      <div className="grid grid-cols-3 gap-2">
        <div>
          <Label htmlFor="git-branch">branch</Label>
          <Input id="git-branch" value={branch} onChange={e => setBranch(e.target.value)} />
        </div>
        <div className="col-span-2">
          <Label htmlFor="git-path">path</Label>
          <Input id="git-path" value={path} onChange={e => setPath(e.target.value)} />
        </div>
      </div>

      <div>
        <Label htmlFor="git-token">Personal Access Token (private repo만)</Label>
        <div className="relative">
          <Input
            id="git-token"
            type={tokenVisible ? 'text' : 'password'}
            value={token}
            onChange={e => setToken(e.target.value)}
            placeholder="ghp_..."
            autoComplete="off"
            className="pr-10"
          />
          <button
            type="button"
            onClick={() => setTokenVisible(v => !v)}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground"
            aria-label={tokenVisible ? '토큰 숨기기' : '토큰 표시'}
          >
            {tokenVisible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </button>
        </div>
        <p className="text-[12px] text-muted-foreground mt-1">토큰은 한 번만 사용되고 저장되지 않습니다</p>
      </div>

      <div>
        <Label htmlFor="git-name">stack 이름 *</Label>
        <Input id="git-name" value={name} onChange={e => setName(e.target.value)} placeholder="my-stack" />
        {errors.name
          ? <p className="text-[12px] text-destructive mt-1">{errors.name}</p>
          : <p className="text-[12px] text-muted-foreground mt-1">소문자/숫자/하이픈만, 1-50자</p>}
      </div>

      {errors._form && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-[12px] text-destructive">
          {errors._form}
        </div>
      )}

      <div className="flex justify-end gap-2 pt-2">
        <Button type="button" variant="outline" onClick={onCancel} disabled={submitting}>취소</Button>
        <Button type="submit" disabled={submitting}>
          {submitting ? '가져오는 중…' : '가져오기'}
        </Button>
      </div>
    </form>
  )
}
