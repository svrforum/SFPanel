import { useCallback, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { useConfirm } from '@/components/ConfirmDialog'
import { dropTaskDrafts } from './taskDrafts'
import { listLacks, type ListHolds } from './taskListModel'

/**
 * Deleting a task, from its pane or its editor: asked first, then the list
 * is read again until it no longer has the task (onDeleted), and the page
 * goes back to /cluster/tasks if the operator is still where they asked. A
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
      // Through a follower the list takes a few seconds to lose the task, and
      // the operator may open another one meanwhile. Not this component's
      // being mounted: the pane it sits in is replaced once the list loses
      // the task, which is the wait succeeding.
      const from = window.location.pathname
      setDeleting(true)
      try {
        await api.deleteTask(id)
        dropTaskDrafts(id)
        toast.success(t('cluster.tasks.editor.deleted'))
        await onDeleted(listLacks(id))
        if (!stay && window.location.pathname === from) navigate('/cluster/tasks')
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
