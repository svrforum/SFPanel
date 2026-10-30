import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Trash2 } from 'lucide-react'
import { api, type ApiError } from '@/lib/api'
import type { ClusterTokenInfo } from '@/types/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { toast } from 'sonner'
import { KindBadge } from './KindBadge'

// The join tokens nobody has used yet, of both kinds, each revocable. Closing
// the add-agent dialog leaves its token valid (the command may be pasted
// later), so this is where one that will not be used is taken back. reload
// changes when a token was made elsewhere in the dialog.
export function PendingTokens({ reload }: { reload: number }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [tokens, setTokens] = useState<ClusterTokenInfo[]>([])
  const [ownReload, setOwnReload] = useState(0)

  useEffect(() => {
    let live = true
    api.listClusterTokens()
      .then((res) => { if (live) setTokens(res.tokens || []) })
      .catch((err) => { if (live) toast.error(err instanceof Error ? err.message : String(err)) })
    return () => { live = false }
  }, [reload, ownReload])

  const handleRevoke = async (id: string) => {
    if (!(await confirm({ title: t('cluster.tokens.revokeConfirm'), danger: true }))) return
    try {
      await api.revokeClusterToken(id)
      toast.success(t('cluster.tokens.revoked'))
    } catch (err) {
      // Used or expired meanwhile: it is gone, which is what was asked for.
      if ((err as ApiError).status !== 404) {
        toast.error(err instanceof Error ? err.message : String(err))
        return
      }
    }
    setOwnReload((n) => n + 1)
  }

  return (
    <div className="space-y-2">
      <div>
        <h3 className="text-[13px] font-medium">{t('cluster.tokens.activeTitle')}</h3>
        <p className="text-[11px] text-muted-foreground">{t('cluster.tokens.activeDescription')}</p>
      </div>
      {tokens.length === 0 ? (
        <p className="text-[13px] text-muted-foreground">{t('cluster.tokens.empty')}</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('cluster.tokens.token')}</TableHead>
              <TableHead>{t('cluster.nodes.kind')}</TableHead>
              <TableHead>{t('cluster.tokens.expiresAt')}</TableHead>
              <TableHead className="w-0"><span className="sr-only">{t('cluster.tokens.revoke')}</span></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokens.map((tok) => (
              <TableRow key={tok.id}>
                <TableCell>
                  <div className="font-mono text-[12px] break-all">{tok.masked}</div>
                  <div className="text-[11px] text-muted-foreground">{t('cluster.tokens.createdBy')}: {tok.created_by}</div>
                </TableCell>
                {/* An older leader names no kind: every token it made is a manager token. */}
                <TableCell><KindBadge kind={tok.kind === 'agent' ? 'agent' : 'manager'} /></TableCell>
                <TableCell className="text-[12px] text-muted-foreground">{new Date(tok.expires_at).toLocaleString()}</TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 p-0 text-destructive hover:text-destructive hover:bg-destructive/10"
                    onClick={() => handleRevoke(tok.id)}
                    title={t('cluster.tokens.revoke')}
                    aria-label={t('cluster.tokens.revoke')}
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
  )
}
