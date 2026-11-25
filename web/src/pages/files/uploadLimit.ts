/** The largest upload limit a server takes (max_upload_size, MB); also its default. */
export const UPLOAD_LIMIT_MAX_MB = 1024

const MIB = 1024 * 1024

/** A limit in MB as the server keeps it (a settings value or a refusal's limit_mb), or null for none. */
export function limitFrom(value: string | undefined): number | null {
  if (value === undefined || !/^\d+$/.test(value.trim())) return null
  const mb = Number(value)
  return mb > 0 ? mb : null
}

/**
 * A file the page can tell is over the limit without sending it. The server
 * counts the form's own bytes too, so a file just under the limit is still
 * the server's to refuse.
 */
export function overLimit(size: number, limitMb: number | null): boolean {
  return limitMb !== null && size > limitMb * MIB
}

/**
 * Whether a higher limit could let any of these refused files in: not when
 * the limit is already the most a server takes, nor for files that size or
 * bigger, which no limit lets through.
 */
export function limitCanRise(limitMb: number | null, sizes: number[]): boolean {
  if (limitMb !== null && limitMb >= UPLOAD_LIMIT_MAX_MB) return false
  return sizes.some((size) => size < UPLOAD_LIMIT_MAX_MB * MIB)
}
