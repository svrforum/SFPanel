import { describe, expect, it } from 'vitest'
import ko from './locales/ko.json'
import en from './locales/en.json'
import { CODE_MESSAGES } from '@/lib/codeMessages'

// A key the code asks for and the locale files do not have renders as the key
// itself: the stack delete dialog showed "docker.compose.deleteDataNote".
// This collects every literal t('a.b') in the sources and looks it up in both
// languages. Keys built at runtime (t(`x.${y}`)) are out of its reach.
const sources = import.meta.glob(['../**/*.{ts,tsx}', '!../**/*.test.ts', '!../**/*.test.tsx'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

function leaf(tree: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>(
    (node, part) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined),
    tree,
  )
}

// i18next resolves a count through key_one / key_other.
const forms = (key: string) => [key, `${key}_one`, `${key}_other`]

function has(tree: unknown, key: string): boolean {
  return forms(key).some((k) => typeof leaf(tree, k) === 'string')
}

const used = new Map<string, string>()
// Keys asked for with nothing to fill in: t('a.b') and the call closes.
const bare = new Map<string, string>()
for (const [file, text] of Object.entries(sources)) {
  for (const m of text.matchAll(/\bt\(\s*['"]([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+)['"](\s*\))?/g)) {
    used.set(m[1], file)
    if (m[2]) bare.set(m[1], file)
  }
}

// {{device}}, not Docker's {{.Name}}: i18next prints a placeholder it was
// given no value for as it stands.
const placeholder = /\{\{\s*[A-Za-z_]\w*\s*\}\}/

describe('translation keys', () => {
  it('reads the sources it checks', () => {
    expect(used.size).toBeGreaterThan(1000)
  })

  it.each([['ko', ko], ['en', en]] as const)('every literal key exists in %s', (_lang, tree) => {
    const missing = [...used].filter(([key]) => !has(tree, key)).map(([key, file]) => `${key} (${file})`)
    expect(missing).toEqual([])
  })

  // Error codes are worded through CODE_MESSAGES, which no t('…') scan sees.
  it.each([['ko', ko], ['en', en]] as const)('every error-code message exists in %s', (_lang, tree) => {
    const missing = Object.entries(CODE_MESSAGES).filter(([, key]) => !has(tree, key)).map(([code, key]) => `${code} → ${key}`)
    expect(missing).toEqual([])
  })

  // The format dialog's title read "{{device}} 장치 포맷": the key existed,
  // and the call passed no device.
  it.each([['ko', ko], ['en', en]] as const)('no key asked for without its values has a placeholder in %s', (_lang, tree) => {
    const unfilled = [...bare]
      .filter(([key]) => forms(key).some((k) => placeholder.test(String(leaf(tree, k) ?? ''))))
      .map(([key, file]) => `${key} (${file})`)
    expect(unfilled).toEqual([])
  })
})

// A Korean particle follows the sound the word before it ends on: a version
// ending in 1 takes 로 where one ending in 0 takes 으로, a port number 8080
// reads 팔천팔십 and takes 으로. After a value the string cannot know, it is
// written for both (을(를), 이(가), (으)로) or kept off the value.
function strings(tree: unknown, prefix = ''): [string, string][] {
  if (typeof tree === 'string') return [[prefix, tree]]
  if (!tree || typeof tree !== 'object') return []
  return Object.entries(tree).flatMap(([k, v]) => strings(v, prefix ? `${prefix}.${k}` : k))
}

// Values from a closed set the particle is right for: ⌘V / Ctrl+V end in 브이.
const KNOWN_ENDING = new Set(['terminal.menu.pasteBlocked'])

describe('Korean particles', () => {
  it('none after a value depends on how the value ends', () => {
    const wrong = strings(ko)
      .filter(([key]) => !KNOWN_ENDING.has(key))
      .filter(([, s]) => /\}\} ?(으로|로|을|를|이|가|은|는|과|와)(?=[\s,.!?)…]|$)/.test(s))
      .map(([key, s]) => `${key}: ${s}`)
    expect(wrong).toEqual([])
  })
})
