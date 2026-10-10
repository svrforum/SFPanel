import { describe, expect, it } from 'vitest'
import ko from './locales/ko.json'
import en from './locales/en.json'

// A key the code asks for and the locale files do not have renders as the key
// itself: the stack delete dialog showed "docker.compose.deleteDataNote".
// This collects every literal t('a.b') in the sources and looks it up in both
// languages. Keys built at runtime (t(`x.${y}`)) are out of its reach.
const sources = import.meta.glob(['../**/*.{ts,tsx}', '!../**/*.test.ts', '!../**/*.test.tsx'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

function has(tree: unknown, key: string): boolean {
  const leaf = (k: string) => k.split('.').reduce<unknown>(
    (node, part) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined),
    tree,
  )
  // i18next resolves a count through key_one / key_other.
  return [key, `${key}_one`, `${key}_other`].some((k) => typeof leaf(k) === 'string')
}

const used = new Map<string, string>()
for (const [file, text] of Object.entries(sources)) {
  for (const m of text.matchAll(/\bt\(\s*['"]([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+)['"]/g)) used.set(m[1], file)
}

describe('translation keys', () => {
  it('reads the sources it checks', () => {
    expect(used.size).toBeGreaterThan(1000)
  })

  it.each([['ko', ko], ['en', en]] as const)('every literal key exists in %s', (_lang, tree) => {
    const missing = [...used].filter(([key]) => !has(tree, key)).map(([key, file]) => `${key} (${file})`)
    expect(missing).toEqual([])
  })
})
