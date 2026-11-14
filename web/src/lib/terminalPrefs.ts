import type { TerminalSnippet } from '@/types/api'

// The panel's limits (internal/feature/ai prefs.go), which the page keeps to
// so that a save is not refused for a reason it could have named itself.
export const SNIPPETS_MAX = 100
export const SNIPPET_BODY_MAX = 2000

/** A text as it is kept: one kind of line break, no blank edges. '' is nothing to keep. */
export function snippetBody(text: string): string {
  return text.replace(/\r\n?/g, '\n').trim()
}

/** Why a text cannot be added to the list, or null: it can. */
export function snippetRefusal(list: TerminalSnippet[], snippet: TerminalSnippet): 'empty' | 'long' | 'there' | 'full' | null {
  if (!snippet.body) return 'empty'
  if ([...snippet.body].length > SNIPPET_BODY_MAX) return 'long'
  if (list.some((s) => s.kind === snippet.kind && s.body === snippet.body)) return 'there'
  if (list.length >= SNIPPETS_MAX) return 'full'
  return null
}
