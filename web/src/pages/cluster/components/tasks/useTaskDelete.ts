import { useCallback, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { useConfirm } from '@/components/ConfirmDialog'
import { dropTaskDrafts } from './taskDrafts'

/**
 * Deleting a task, from its pane or its editor: asked first, then the list
 * is read again (onDeleted) and the page goes back to /cluster/tasks. A
 * built-in cannot be deleted; a Git task can (its runs keep their revision).
 */
export function useTaskDelete(onDeleted: () => Promise<void> | void) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const navigate = useNavigate()
  const [deleting, setDeleting] = useState(false)

  const remove = useCallback(
    async (id: string, name: string) => {
      const yes = await confirm({
        title: t('cluster.tasks.editor.deleteTitle', { name }),
        description: t('cluster.tasks.editor.deleteDescription'),
        confirmLabel: t('common.delete'),
        danger: true,
      })
      if (!yes) return
      setDeleting(true)
      try {
        await api.deleteTask(id)
        dropTaskDrafts(id)
        toast.success(t('cluster.tasks.editor.deleted'))
        await onDeleted()
        navigate('/cluster/tasks')
      } catch (err) {
        toast.error(taskErrorMessage(err, t))
      } finally {
        setDeleting(false)
      }
    },
    [confirm, navigate, onDeleted, t],
  )

  return { remove, deleting }
}
