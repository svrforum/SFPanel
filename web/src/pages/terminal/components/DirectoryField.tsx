import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { dirName, filterDirs, mergeDirs, type DirSource } from '@/lib/dirSuggestions'
import type { AIDirs } from '@/types/api'

// The directory field of the new-session dialog: free text, with the server's
// suggestions in a list the page draws itself. It used to be an <input list>
// with a <datalist>, which the browser renders natively — on Android that is
// an unstyled sheet the page cannot size or place, so it covered the dialog,
// the key bar and the keyboard, and showed a directory twice when it was both
// recent and a stack. This list lives inside the dialog, scrolls on its own,
// and is filtered only by what was typed since the field took focus: the
// prefilled path would otherwise narrow it to the one entry already chosen.
export function DirectoryField({
  id,
  value,
  onChange,
  dirs,
  placeholder,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  dirs: AIDirs | null
  placeholder?: string
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(-1)
  const all = useMemo(() => mergeDirs(dirs), [dirs])
  const shown = useMemo(() => filterDirs(all, query), [all, query])
  const expanded = open && shown.length > 0
  const listId = `${id}-suggestions`
  const optionId = (i: number) => `${listId}-${i}`
  const label: Record<DirSource, string> = {
    recent: t('ai.dialog.dirRecent'),
    stack: t('ai.dialog.dirStacks'),
    home: t('ai.dialog.dirHome'),
  }

  // Arrow keys move the highlight past the list's visible rows; follow it.
  useEffect(() => {
    if (expanded && active >= 0) document.getElementById(`${listId}-${active}`)?.scrollIntoView({ block: 'nearest' })
  }, [active, expanded, listId])

  const pick = (path: string) => {
    onChange(path)
    setOpen(false)
    setQuery('')
    setActive(-1)
  }

  return (
    <div>
      <Input
        id={id}
        role="combobox"
        aria-expanded={expanded}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={expanded && active >= 0 ? optionId(active) : undefined}
        // Keeps the browser's own autofill sheet from reappearing where the
        // datalist used to be.
        autoComplete="off"
        spellCheck={false}
        value={value}
        placeholder={placeholder}
        className="font-mono text-[12px]"
        onFocus={() => { setOpen(true); setQuery(''); setActive(-1) }}
        onBlur={() => setOpen(false)}
        onChange={(e) => { onChange(e.target.value); setQuery(e.target.value); setOpen(true); setActive(-1) }}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') {
            e.preventDefault()
            if (!open) { setOpen(true); return }
            setActive((a) => Math.min(a + 1, shown.length - 1))
          } else if (e.key === 'ArrowUp') {
            e.preventDefault()
            setActive((a) => Math.max(a - 1, 0))
          } else if (e.key === 'Enter' && expanded && active >= 0) {
            // Picks the highlighted directory instead of submitting the form.
            e.preventDefault()
            pick(shown[active].path)
          } else if (e.key === 'Escape' && expanded) {
            setOpen(false)
          }
        }}
      />
      {expanded && (
        <ul
          id={listId}
          role="listbox"
          aria-label={t('ai.dialog.dirSuggestions')}
          className="mt-1.5 max-h-52 overflow-y-auto overscroll-contain rounded-xl border border-border bg-card p-1 shadow-sm"
          // Keep focus in the field so a tap picks rather than blurring the
          // field and closing the list before the click lands.
          onMouseDown={(e) => e.preventDefault()}
        >
          {shown.map((s, i) => {
            const name = dirName(s.path)
            const parent = s.path.slice(0, s.path.length - name.length)
            return (
              <li
                key={s.path}
                id={optionId(i)}
                role="option"
                aria-selected={i === active}
                onClick={() => pick(s.path)}
                onMouseMove={() => { if (active !== i) setActive(i) }}
                className={cn(
                  'flex min-h-9 cursor-pointer items-center gap-2 rounded-lg px-2.5 pointer-coarse:min-h-11',
                  'aria-selected:bg-accent',
                )}
              >
                <span className="flex min-w-0 flex-1 font-mono text-[12px]">
                  <span className="min-w-0 truncate text-muted-foreground">{parent}</span>
                  <span className="shrink-0 text-foreground">{name}</span>
                </span>
                <span className="shrink-0 text-[11px] text-muted-foreground">
                  {s.sources.map((src) => label[src]).join(' · ')}
                </span>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
