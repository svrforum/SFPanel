/**
 * A whole number typed into a setting field, or null when it is not one or is below min.
 *
 * parseInt read "1.5" as 1 and "12abc" as 12, so a value the operator never
 * typed was saved; anything that is not a whole number is refused instead.
 */
export function parseSettingNumber(raw: string, min: number): number | null {
  const text = raw.trim()
  if (!/^-?\d+$/.test(text)) return null
  const value = Number(text)
  return value >= min ? value : null
}
