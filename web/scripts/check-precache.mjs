// The service worker precaches what workbox's globPatterns match less its
// globIgnores (vite.config.ts), and those ignores name Monaco's chunks. A
// renamed chunk slips past them in silence: rolldown 1.1's names did, and
// monaco-editor 0.57's did again, and each time every first visit downloaded
// the 2.7 MB editor up front. A size budget does not care what a chunk is
// called. Run after `npm run build`.
import { readFileSync, statSync } from 'node:fs'

const BUDGET = 3.5 * 1024 * 1024
const dist = new URL('../dist/', import.meta.url)
const sw = readFileSync(new URL('sw.js', dist), 'utf8')
const files = [...sw.matchAll(/url:"([^"]+)"/g)].map(([, url]) => ({ url, size: statSync(new URL(url, dist)).size }))
const total = files.reduce((sum, f) => sum + f.size, 0)
const kib = (n) => `${Math.round(n / 1024)} KiB`

if (total > BUDGET) {
  console.error(`The service worker precaches ${kib(total)}, past the ${kib(BUDGET)} budget. The largest:`)
  for (const f of files.sort((a, b) => b.size - a.size).slice(0, 5)) console.error(`  ${kib(f.size)}  ${f.url}`)
  console.error('A large chunk renamed by a dependency usually belongs in globIgnores in vite.config.ts.')
  process.exit(1)
}
console.log(`precache: ${files.length} files, ${kib(total)} (budget ${kib(BUDGET)})`)
