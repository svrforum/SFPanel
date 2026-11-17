/** Moves focus to the first of the fields that is missing. */
export function focusFirst(fields: [id: string, missing: boolean][]) {
  const first = fields.find(([, missing]) => missing)
  if (first) document.getElementById(first[0])?.focus()
}
