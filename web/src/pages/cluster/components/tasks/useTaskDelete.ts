import { useCallback, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { useConfirm } from '@/components/ConfirmDialog'
import { dropTaskDrafts } from './taskDrafts'
import { listLacks, type ListHolds } from './taskList'

/**
 * Deleting a task, from its pane or its editor: asked first, then the list
 * is read again until it no longer has the task (onDeleted), and the page
 * goes back to /cluster/tasks. A
 * built-in cannot be deleted; a Git task can (its runs keep their revision).
 * stay keeps the page where it is: the Git import deletes a task gone from
 * its repository from inside its dialog. remove answers whether it deleted.
 */
export function useTaskDelete(onDeleted: (holds?: ListHolds) => Promise<void> | void, { stay = false }: { stay?: boolean } = {}) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const navigate = useNavigate()
  const [deleting, setDeleting] = useState(false)

  const remove = useCallback(
    async (id: string, name: string): Promise<boolean> => {
      const yes = await confirm({
        title: t('cluster.tasks.editor.deleteTitle', { name }),
        description: t('cluster.tasks.editor.deleteDescription'),
        confirmLabel: t('common.delete'),
        danger: true,
      })
      if (!yes) return false
      setDeleting(true)
      try {
        await api.deleteTask(id)
        dropTaskDrafts(id)
        toast.success(t('cluster.tasks.editor.deleted'))
        await onDeleted(listLacks(id))
        if (!stay) navigate('/cluster/tasks')
        return true
      } catch (err) {
        toast.error(taskErrorMessage(err, t))
        return false
      } finally {
        setDeleting(false)
      }
    },
    [confirm, navigate, onDeleted, stay, t],
  )

  return { remove, deleting }
}
