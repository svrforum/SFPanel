import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowLeft, ArrowRight, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import {
  DEFAULT_KEYSETS, KEY_CATALOG, KEY_LABEL_MAX, KEY_TEXT_MAX, LONG_LABEL, PAGES_MAX, PAGE_KEYS,
  barKey, barKeyName, isDefaultKeyset, longLabel, storedPages,
  type KeysetKind, type StoredKey, type StoredKeysets,
} from '@/lib/terminalKeysets'

const KINDS: KeysetKind[] = ['agent', 'shell']
const CHIP = 'min-h-11 min-w-0 overflow-hidden rounded-lg border border-border bg-secondary text-[11px] font-semibold text-foreground active:bg-accent focus-visible:outline-2 focus-visible:outline-ring'
const GROUPS = { basic: 'groupBasic', move: 'groupMove', modifier: 'groupModifier', control: 'groupControl' } as const
const copy = (pages: StoredKey[][]): StoredKey[][] => pages.map((page) => [...page])
const chars = (s: string) => [...s].length

/**
 * Arranging the key row (lib/terminalKeysets): which keys, on which page, in
 * which order, for each kind of session. The keys come from the catalog or are
 * the operator's own — a text the key types, with Enter after it when asked.
 *
 * Nothing is kept until Save. An arrangement that is the panel's own is saved
 * as nothing at all, so a later panel's better defaults reach whoever never
 * changed theirs.
 *
 * Mounted while it is open: what it starts from is read once.
 */
export function KeysEditor({ kind, saved, onSave, onClose }: {
  /** the session on screen: the editor opens on its keys */
  kind: KeysetKind
  saved: StoredKeysets
  /** answers whether the arrangement was kept */
  onSave: (next: StoredKeysets) => Promise<boolean>
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [tab, setTab] = useState<KeysetKind>(kind)
  const [draft, setDraft] = useState<Record<KeysetKind, StoredKey[][]>>(() => ({
    agent: copy(storedPages('agent', saved)),
    shell: copy(storedPages('shell', saved)),
  }))
  // The key being moved or removed, and the page a new key goes to.
  const [picked, setPicked] = useState<{ page: number; index: number } | null>(null)
  const [adding, setAdding] = useState<number | null>(null)
  const [own, setOwn] = useState({ text: '', label: '', enter: false })
  const [saving, setSaving] = useState(false)

  const pages = draft[tab]
  const change = (next: StoredKey[][]) => setDraft((d) => ({ ...d, [tab]: next }))
  const openTab = (next: KeysetKind) => {
    setTab(next)
    setPicked(null)
    setAdding(null)
  }

  // Within its page a key swaps with its neighbour; at the page's edge it
  // goes to the next page's near end, when that page has room.
  const destination = (dir: -1 | 1): { page: number; index: number } | null => {
    if (!picked) return null
    const index = picked.index + dir
    if (index >= 0 && index < pages[picked.page].length) return { page: picked.page, index }
    const page = picked.page + dir
    if (page < 0 || page >= pages.length || pages[page].length >= PAGE_KEYS) return null
    return { page, index: dir > 0 ? 0 : pages[page].length }
  }
  const move = (dir: -1 | 1) => {
    const to = destination(dir)
    if (!picked || !to) return
    const next = copy(pages)
    const [key] = next[picked.page].splice(picked.index, 1)
    next[to.page].splice(to.index, 0, key)
    change(next)
    setPicked(to)
  }
  const remove = () => {
    if (!picked) return
    const next = copy(pages)
    next[picked.page].splice(picked.index, 1)
    change(next)
    setPicked(null)
  }
  const add = (key: StoredKey) => {
    if (adding === null || pages[adding].length >= PAGE_KEYS) return
    const next = copy(pages)
    next[adding].push(key)
    change(next)
    // A full page takes no more.
    if (next[adding].length >= PAGE_KEYS) setAdding(null)
  }
  const addPage = () => {
    change([...copy(pages), []])
    setPicked(null)
    setAdding(pages.length)
  }
  const removePage = (page: number) => {
    change(copy(pages).filter((_, i) => i !== page))
    setPicked(null)
    setAdding(null)
  }

  // A key of the operator's own. The label is the text itself where that fits.
  const ownLabel = own.label.trim() || (chars(own.text) <= KEY_LABEL_MAX ? own.text.trim() : '')
  const ownReady = own.text !== '' && ownLabel !== '' && chars(ownLabel) <= KEY_LABEL_MAX && chars(own.text) <= KEY_TEXT_MAX
  const addOwn = () => {
    if (!ownReady) return
    add({ label: ownLabel, text: own.text, ...(own.enter ? { enter: true } : {}) })
    setOwn({ text: '', label: '', enter: false })
  }

  const save = async () => {
    const next: StoredKeysets = {}
    for (const k of KINDS) {
      const filled = draft[k].filter((page) => page.length > 0)
      if (filled.length > 0 && !isDefaultKeyset(k, filled)) next[k] = filled
    }
    setSaving(true)
    const kept = await onSave(next)
    setSaving(false)
    if (!kept) {
      toast.error(t('terminal.input.saveFailed'))
      return
    }
    toast.success(t('terminal.keysEditor.saved'))
    onClose()
  }

  const drawn = (key: StoredKey) => {
    const bar = barKey(key)
    const look = (label: string, danger: boolean) => cn(/^[↑↓←→⏎]$/.test(label) && 'text-[15px] font-normal', longLabel(label) && LONG_LABEL, danger && 'text-destructive')
    return bar ? { label: bar.label, name: barKeyName(bar, t), look: look(bar.label, bar.danger === true) } : { label: '?', name: '?', look: '' }
  }

  return (
    <Sheet open onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="bottom" data-keys-editor className="max-h-[92dvh] gap-0 overflow-hidden rounded-t-2xl p-0">
        {/* The title, whose keys, and the buttons below stay; the pages between them scroll. */}
        <div className="flex shrink-0 flex-col gap-2 p-4 pb-3">
        <SheetTitle>{t('terminal.keysEditor.title')}</SheetTitle>
        <SheetDescription className="text-[13px]">{t('terminal.keysEditor.hint')}</SheetDescription>
        <div role="group" aria-label={t('terminal.keysEditor.kind')} className="grid grid-cols-2 gap-1 rounded-xl bg-secondary p-1">
          {KINDS.map((k) => (
            <button key={k} type="button" aria-pressed={tab === k} onClick={() => openTab(k)}
              className={cn('min-h-10 rounded-lg text-[14px] font-medium focus-visible:outline-2 focus-visible:outline-ring', tab === k ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground')}>
              {t(k === 'agent' ? 'terminal.keysEditor.kindAgent' : 'terminal.keysEditor.kindShell')}
            </button>
          ))}
        </div>
        </div>

        <div data-keys-body className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4 pb-4">
        {pages.map((page, p) => (
          <section key={p} data-keys-page={p + 1} aria-label={t('terminal.keysEditor.page', { n: p + 1 })} className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <h3 className="text-[12px] font-semibold text-muted-foreground">{t('terminal.keysEditor.page', { n: p + 1 })}</h3>
              {pages.length > 1 && (
                <button type="button" onClick={() => removePage(p)} aria-label={t('terminal.keysEditor.removePage', { n: p + 1 })}
                  className="flex h-9 w-9 items-center justify-center rounded-lg text-muted-foreground active:bg-accent focus-visible:outline-2 focus-visible:outline-ring">
                  <Trash2 className="h-4 w-4" aria-hidden="true" />
                </button>
              )}
            </div>
            <div className="grid grid-cols-7 gap-1">
              {page.map((key, i) => {
                const k = drawn(key)
                const on = picked?.page === p && picked.index === i
                return (
                  <button key={i} type="button" aria-label={k.name} aria-pressed={on}
                    onClick={() => { setPicked(on ? null : { page: p, index: i }); setAdding(null) }}
                    className={cn(CHIP, k.look, on && 'border-primary bg-primary text-primary-foreground')}>
                    {k.label}
                  </button>
                )
              })}
              {page.length < PAGE_KEYS && (
                <button type="button" aria-label={t('terminal.keysEditor.addKey', { n: p + 1 })} aria-pressed={adding === p}
                  onClick={() => { setAdding(adding === p ? null : p); setPicked(null) }}
                  className={cn(CHIP, 'flex items-center justify-center border-dashed bg-transparent text-muted-foreground', adding === p && 'border-primary text-primary')}>
                  <Plus className="h-4 w-4" aria-hidden="true" />
                </button>
              )}
            </div>
            {page.length === 0 && adding !== p && <p className="text-[12px] text-muted-foreground">{t('terminal.keysEditor.empty')}</p>}

            {picked?.page === p && (
              <div className="grid grid-cols-3 gap-1">
                <Button type="button" variant="outline" className="min-h-11 rounded-xl" disabled={!destination(-1)} onClick={() => move(-1)}>
                  <ArrowLeft className="mr-1 h-4 w-4" aria-hidden="true" />{t('terminal.keysEditor.earlier')}
                </Button>
                <Button type="button" variant="outline" className="min-h-11 rounded-xl" disabled={!destination(1)} onClick={() => move(1)}>
                  {t('terminal.keysEditor.later')}<ArrowRight className="ml-1 h-4 w-4" aria-hidden="true" />
                </Button>
                <Button type="button" variant="outline" className="min-h-11 rounded-xl text-destructive" onClick={remove}>
                  <Trash2 className="mr-1 h-4 w-4" aria-hidden="true" />{t('terminal.keysEditor.remove')}
                </Button>
              </div>
            )}

            {adding === p && (
              <div data-keys-picker role="group" aria-label={t('terminal.keysEditor.pick', { n: p + 1 })} className="flex flex-col gap-2 rounded-xl border border-border p-1.5">
                {KEY_CATALOG.map(({ group, ids }) => (
                  <div key={group} className="flex flex-col gap-1">
                    <h4 className="text-[12px] font-semibold text-muted-foreground">{t(`terminal.keysEditor.${GROUPS[group]}`)}</h4>
                    <div className="grid grid-cols-7 gap-1">
                      {ids.map((id) => {
                        const k = drawn({ id })
                        return (
                          <button key={id} type="button" aria-label={k.name} onClick={() => add({ id })} className={cn(CHIP, k.look)}>
                            {k.label}
                          </button>
                        )
                      })}
                    </div>
                  </div>
                ))}
                <div className="flex flex-col gap-1.5">
                  <h4 className="text-[12px] font-semibold text-muted-foreground">{t('terminal.keysEditor.own')}</h4>
                  <Input value={own.text} maxLength={KEY_TEXT_MAX * 2} onChange={(e) => setOwn({ ...own, text: e.target.value })}
                    aria-label={t('terminal.keysEditor.ownText')} placeholder={t('terminal.keysEditor.ownText')}
                    autoCapitalize="off" autoCorrect="off" spellCheck={false} className="h-11 rounded-xl text-[16px]" />
                  <div className="flex items-center gap-1.5">
                    <Input value={own.label} maxLength={KEY_LABEL_MAX * 2} onChange={(e) => setOwn({ ...own, label: e.target.value })}
                      aria-label={t('terminal.keysEditor.ownLabel')} placeholder={`${t('terminal.keysEditor.ownLabel')} (${t('terminal.keysEditor.ownLabelHint')})`}
                      autoCapitalize="off" autoCorrect="off" spellCheck={false} className="h-11 min-w-0 flex-1 rounded-xl text-[16px]" />
                    <Button type="button" className="min-h-11 rounded-xl" disabled={!ownReady} onClick={addOwn}>{t('terminal.keysEditor.ownAdd')}</Button>
                  </div>
                  <label className="flex min-h-11 items-center gap-3 text-[14px]">
                    <Checkbox checked={own.enter} onCheckedChange={(v) => setOwn({ ...own, enter: v === true })} />
                    {t('terminal.keysEditor.ownEnter')}
                  </label>
                </div>
                <Button type="button" variant="outline" className="min-h-11 rounded-xl" onClick={() => setAdding(null)}>{t('terminal.keysEditor.pickDone')}</Button>
              </div>
            )}
          </section>
        ))}

        {pages.length < PAGES_MAX && (
          <Button type="button" variant="outline" className="min-h-11 rounded-xl" onClick={addPage}>
            <Plus className="mr-1 h-4 w-4" aria-hidden="true" />{t('terminal.keysEditor.addPage')}
          </Button>
        )}
        </div>
        <div className="grid shrink-0 grid-cols-2 gap-2 border-t border-border bg-background p-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
          <Button type="button" variant="outline" className="min-h-11 rounded-xl" disabled={isDefaultKeyset(tab, pages)}
            onClick={() => { change(copy(DEFAULT_KEYSETS[tab])); setPicked(null); setAdding(null) }}>
            {t('terminal.keysEditor.reset')}
          </Button>
          <Button type="button" className="min-h-11 rounded-xl" disabled={saving} onClick={() => void save()}>{t('terminal.keysEditor.save')}</Button>
        </div>
      </SheetContent>
    </Sheet>
  )
}
