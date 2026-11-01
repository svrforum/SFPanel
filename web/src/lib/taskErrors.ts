import type { TFunction } from 'i18next'

// What a failed task call reads as. Two answers carry nothing worth showing
// as sent:
//   - 503: the task routes answer INTERNAL_ERROR in one of three English
//     sentences while the leader changes or catches up. Nothing was written;
//     the operator tries again shortly. The page does not retry by itself.
//   - 413 with no code: Echo refuses a body over 1 MiB before the handler,
//     with no envelope, so the client knows only "HTTP 413".
// Any other refusal was already worded by its code (CODE_MESSAGES) on its
// way out of the client, with its details filled in.
export function taskErrorMessage(err: unknown, t: TFunction): string {
  const { status, code } = (err ?? {}) as { status?: number; code?: string }
  if (status === 503) return t('cluster.tasks.errors.leaderBusy')
  if (status === 413 && !code) return t('cluster.tasks.errors.tooLarge')
  return err instanceof Error ? err.message : String(err)
}
