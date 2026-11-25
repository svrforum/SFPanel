// Which log source the Logs screen opens on. The address may ask for one
// (`/logs?source=firewall`); a source this server does not have is never read,
// so an unknown or missing one falls back to the first source that exists.
export function pickLogSource(sources: { id: string; exists: boolean }[], requested: string | null): string | null {
  const existing = sources.filter((s) => s.exists)
  return (existing.find((s) => s.id === requested) ?? existing[0])?.id ?? null
}
