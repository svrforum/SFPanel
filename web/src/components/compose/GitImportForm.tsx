import { useState } from 'react'
import { useTranslation } from 'react-i18next'
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
  const { t } = useTranslation()
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
    if (!url) e.url = t('compose.gitImport.urlRequired', 'Enter a URL')
    else if (!GITHUB_URL_RE.test(url)) e.url = t('compose.gitImport.urlInvalid', 'Only GitHub HTTPS URLs are supported')
    if (!name) e.name = t('compose.gitImport.nameRequired', 'Enter a stack name')
    else if (!STACK_NAME_RE.test(name)) e.name = t('compose.gitImport.nameHint', 'Lowercase letters/digits/hyphens, 1-50 chars')
    setErrors(e)
    return Object.keys(e).length === 0
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return
    setSubmitting(true)
    try {
      const res = await api.importFromGit({ url, branch, path, token: token || undefined, name })
      toast.success(t('compose.gitImport.importSuccess', "Imported stack '{{name}}'", { name: res.project_name }))
      onSuccess(res.project_name)
    } catch (err) {
      // The api client throws plain Error(message); the backend's messages
      // are already user-facing Korean (per Task 9 handler error mapping).
      // Show all errors as a form-bottom banner.
      const msg = (err as Error).message || t('compose.gitImport.importFailed', 'Import failed')
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
          : <p className="text-[12px] text-muted-foreground mt-1">{t('compose.gitImport.urlInvalid', 'Only GitHub HTTPS URLs are supported')}</p>}
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
        <Label htmlFor="git-token">{t('compose.gitImport.tokenLabel', 'Personal Access Token (private repos only)')}</Label>
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
            className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
            aria-label={tokenVisible ? t('compose.gitImport.tokenHide', 'Hide token') : t('compose.gitImport.tokenShow', 'Show token')}
          >
            {tokenVisible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </button>
        </div>
        <p className="text-[12px] text-muted-foreground mt-1">{t('compose.gitImport.tokenHint', 'The token is used once and never stored')}</p>
      </div>

      <div>
        <Label htmlFor="git-name">{t('compose.gitImport.nameLabel', 'Stack name')} *</Label>
        <Input id="git-name" value={name} onChange={e => setName(e.target.value)} placeholder="my-stack" />
        {errors.name
          ? <p className="text-[12px] text-destructive mt-1">{errors.name}</p>
          : <p className="text-[12px] text-muted-foreground mt-1">{t('compose.gitImport.nameHint', 'Lowercase letters/digits/hyphens, 1-50 chars')}</p>}
      </div>

      {errors._form && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-[12px] text-destructive">
          {errors._form}
        </div>
      )}

      <div className="flex justify-end gap-2 pt-2">
        <Button type="button" variant="outline" onClick={onCancel} disabled={submitting}>{t('common.cancel')}</Button>
        <Button type="submit" disabled={submitting}>
          {submitting ? t('compose.gitImport.importing', 'Importing…') : t('compose.gitImport.import', 'Import')}
        </Button>
      </div>
    </form>
  )
}
