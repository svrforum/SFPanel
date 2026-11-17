import { expect, it } from 'vitest'
import type { ConfirmOptions } from './ConfirmDialog'

// The default button says "Confirm". On a confirmation that destroys something
// that word is all some people read, so the type refuses a destructive one
// that does not name its button. The directive below is the test: if the type
// stops refusing, the type-check fails on an unused @ts-expect-error.
it('holds a destructive confirmation to naming its button', () => {
  // @ts-expect-error danger without confirmLabel
  const unnamed: ConfirmOptions = { title: 'Delete rule #3', danger: true }
  const named: ConfirmOptions = { title: 'Delete rule #3', confirmLabel: 'Delete rule', danger: true }
  const plain: ConfirmOptions = { title: 'Transfer leadership?' }
  expect([unnamed.title, named.confirmLabel, plain.confirmLabel]).toEqual(['Delete rule #3', 'Delete rule', undefined])
})
