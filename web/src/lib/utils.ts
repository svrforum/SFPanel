import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatBytes(bytes: number): string {
  if (!bytes || bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return `${size.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function formatDate(value: string | number): string {
  const date = typeof value === 'number'
    ? new Date(value * 1000)
    : new Date(value)
  if (isNaN(date.getTime())) return '-'
  return date.toLocaleString()
}

export function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

export function getUsageColor(percent: number, variant?: 'cpu' | 'mem' | 'swap'): string {
  if (percent > 80) return '#f04452'
  if (percent > 50) return '#f59e0b'
  if (variant === 'mem') return '#00c471'
  return '#3182f6'
}

// copyText copies text to the clipboard and returns whether it succeeded.
//
// The async Clipboard API (navigator.clipboard) is only exposed in a SECURE
// context — HTTPS or localhost. The panel is routinely served plain HTTP over a
// LAN IP (TLS is the reverse proxy's job), where navigator.clipboard is
// undefined and every copy button silently fails. So we try the modern API when
// available and fall back to a hidden <textarea> + execCommand('copy'), which
// works in non-secure contexts as long as it runs inside a user gesture (our
// callers all fire from onClick).
export async function copyText(text: string): Promise<boolean> {
  try {
    if (window.isSecureContext && navigator.clipboard) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // Permission denied or unavailable — fall through to the legacy path.
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.top = '-9999px'
    ta.style.left = '-9999px'
    document.body.appendChild(ta)
    ta.select()
    ta.setSelectionRange(0, text.length)
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

// POSIX-style path helpers shared by the file/disk browsers (Files, DiskUsage)
// — each used to carry its own copies.
export function pathBasename(p: string): string {
  if (p === '/') return '/'
  const parts = p.replace(/\/+$/, '').split('/')
  return parts[parts.length - 1] || '/'
}

export function pathParent(p: string): string {
  const cleaned = p.replace(/\/+$/, '')
  const idx = cleaned.lastIndexOf('/')
  if (idx <= 0) return '/'
  return cleaned.slice(0, idx)
}

export function pathJoin(...parts: string[]): string {
  const [first, ...rest] = parts
  const joined = [first.replace(/\/+$/, ''), ...rest.map((p) => p.replace(/^\/+/, ''))].join('/')
  return joined.replace(/\/+/g, '/') || '/'
}

// Single source for the cluster node status → color-class mapping. The same
// switch used to be copy-pasted in five components (NodeSelector, MoreMenu,
// cluster TreePanel, ClusterNodes, ClusterOverview) and had already grown a
// CSS-variable variant — one function keeps a future status grade or color
// change from drifting across surfaces.
export function nodeStatusColor(status: string): string {
  switch (status) {
    case 'online':
      return 'bg-success'
    case 'suspect':
      return 'bg-warning'
    case 'offline':
      return 'bg-destructive'
    default:
      return 'bg-muted-foreground'
  }
}

// Trigger a browser download for an in-memory blob. Callers used to inline the
// createElement('a') + objectURL dance (Security 2FA codes, Maintenance
// backups) — the revoke in a finally keeps the object URL from leaking when
// click() throws.
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
  } finally {
    URL.revokeObjectURL(url)
  }
}
