import { api } from '@/lib/api'
import { toast } from 'sonner'

/**
 * saveSetting — single-key wrapper over api.updateSettings used by
 * NodeSettingField in its two places: the terminal's Tools & accounts sheet
 * (terminal timeout) and the file manager's upload limit dialog (max upload
 * size). Both share the same toast/error shape, so callers don't repeat it.
 *
 * Validation stays in the caller (caller knows what counts as valid for
 * its specific field). This helper only handles the HTTP call + toast.
 *
 * @returns true on success, false on failure (handler can use this to gate
 *          state updates).
 */
export async function saveSetting(
  key: 'terminal_timeout' | 'max_upload_size',
  value: string,
  messages: { success: string; failure: string },
): Promise<boolean> {
  try {
    await api.updateSettings({ [key]: value })
    toast.success(messages.success)
    return true
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : messages.failure
    toast.error(message)
    return false
  }
}
