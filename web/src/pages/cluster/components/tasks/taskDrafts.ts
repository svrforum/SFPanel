import type { TaskModel, TaskView } from '@/types/api'
import { blankTask, hasYamlComments } from './taskForm'

// Drafts of the task editor, kept while the panel stays open: going to
// another menu and back finds the change where it was left. A reload or a
// closed tab loses them, which is why the editor asks first (beforeunload).
// Keyed by what the editor was opened for: edit:<id>, or new:<the task a
// clone was taken from, or nothing>.

export type EditorView = 'form' | 'yaml'

export interface TaskDraft {
  view: EditorView
  model: TaskModel
  /** One React key per step, so a card keeps its fields when steps move. */
  keys: number[]
  yaml: string
  /** The YAML format gave for model, while neither has changed since. */
  formatted: { yaml: string; model: TaskModel } | null
}

const drafts = new Map<string, TaskDraft>()

export const draftKey = (taskId: string | undefined, from: string | null) => (taskId ? `edit:${taskId}` : `new:${from ?? ''}`)

export function readDraft(key: string): TaskDraft | undefined {
  return drafts.get(key)
}

export function keepDraft(key: string, draft: TaskDraft): void {
  drafts.set(key, draft)
}

export function dropDraft(key: string): void {
  drafts.delete(key)
}

/** A task deleted: its edit draft goes with it. */
export function dropTaskDrafts(taskId: string): void {
  drafts.delete(draftKey(taskId, null))
}

/** A model to edit in the form. */
export function draftFromModel(model: TaskModel): TaskDraft {
  return { view: 'form', model, keys: model.steps.map((_, i) => i), yaml: '', formatted: null }
}

/**
 * A stored task to edit. It opens in the form when its document parses and
 * has no comment the form would lose; otherwise in YAML, as it is written.
 * The stored document is the YAML of its model until either changes.
 */
export function draftFromTask(view: TaskView): TaskDraft {
  const yaml = view.yaml ?? ''
  const model = view.task
  if (model && !hasYamlComments(yaml)) return { ...draftFromModel(model), yaml, formatted: { yaml, model } }
  const fallback = model ?? blankTask()
  return { view: 'yaml', model: fallback, keys: fallback.steps.map((_, i) => i), yaml, formatted: null }
}
