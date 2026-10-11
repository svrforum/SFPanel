import { toast } from 'sonner'
import type { TFunction } from 'i18next'
import { CODE_MESSAGES } from './codeMessages'

// A problem beside a result that did happen — a repository that refused apt
// while the install went ahead on the others. Worded by its code like an
// error, with its details filling in the message.
export interface ApiWarning {
  code: string
  message: string
  details?: Record<string, string>
}

export function warningText(w: ApiWarning, t: TFunction): string {
  const key = CODE_MESSAGES[w.code]
  return key ? t(key, { ...w.details, defaultValue: w.message }) : w.message
}

export function toastWarnings(warnings: ApiWarning[] | undefined | null, t: TFunction): void {
  for (const w of warnings ?? []) toast.warning(warningText(w, t))
}
