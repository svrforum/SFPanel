import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUp, Keyboard, PenLine, Plus } from 'lucide-react'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { MODIFIERS_CONSUMED_EVENT, MODIFIERS_EVENT, terminalKey } from '@/lib/terminalKeys'
import { KEYSETS, NO_BAR_MODIFIERS, activeModifiers, afterKey, tapModifier, type BarKey, type BarModifiers, type KeysetKind } from '@/lib/terminalKeysets'

/** What the Android app reaches on the input area's element. */
export interface TerminalInputElement extends HTMLElement {
  /** adds text to the draft and gives the composer the keyboard ("paste and edit") */
  __compose?: (text: string) => void
}

const SEND_ENTER_KEY = 'sfpanel_terminal_send_enter'
const recentKey = () => `sfpanel_terminal_recent:${api.currentNode || 'local'}`
const RECENT_MAX = 20
// A held arrow repeats after this long, and then this often.
const REPEAT_DELAY = 380
const REPEAT_EVERY = 90
// A sideways drag this long across the keys turns the page.
const SWIPE = 48

function readJSON<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key)
    return raw === null ? fallback : (JSON.parse(raw) as T)
  } catch { return fallback }
}
function writeJSON(key: string, value: unknown) {
  try { localStorage.setItem(key, JSON.stringify(value)) } catch { /* private mode */ }
}

const KEY = 'min-w-0 min-h-12 short:min-h-9 rounded-lg border border-border bg-secondary text-[13px] font-semibold text-foreground select-none active:bg-accent focus-visible:outline-2 focus-visible:outline-ring'
const ROUND = 'flex h-12 w-12 short:h-9 short:w-9 shrink-0 items-center justify-center rounded-xl border border-border bg-secondary text-foreground active:bg-accent focus-visible:outline-2 focus-visible:outline-ring'

/**
 * The input area of the compact layout (lib/terminalLayout): a composer and
 * one row of keys.
 *
 * The composer is a real textarea, so what a phone does for text — an IME
 * composing Korean, dictation, autocorrect, a long press to paste — works as
 * it does anywhere, where the terminal's own hidden field fights each of them.
 * What is written goes to the session at once, as a paste, with the Enter that
 * submits it.
 *
 * The keys are seven to a page and a cell that turns the page, by what the
 * session runs (lib/terminalKeysets). A press never takes the focus: the
 * keyboard stays with the composer or the terminal, whichever had it.
 *
 * `data-mobile-terminal-bar` is what Android apps up to 0.1.6 hide — they draw
 * key rows of their own — so they keep working unchanged; 0.1.7 leaves this
 * one in place and draws none.
 */
export function TerminalInput({ kind, sessionKey, onSendKey, onSubmit }: {
  kind: KeysetKind
  /** whose draft the composer holds; null with no session open */
  sessionKey: string | null
  onSendKey: (data: string) => void
  /** the composed text, and whether the Enter that submits it follows */
  onSubmit: (text: string, enter: boolean) => void
}) {
  const { t } = useTranslation()
  const rootRef = useRef<HTMLDivElement>(null)
  const keysRef = useRef<HTMLDivElement>(null)
  const draftRef = useRef<HTMLTextAreaElement>(null)
  const [mods, setMods] = useState<BarModifiers>(NO_BAR_MODIFIERS)
  const [page, setPage] = useState(0)
  const [sendEnter, setSendEnter] = useState(() => readJSON(SEND_ENTER_KEY, true))
  const [recent, setRecent] = useState<string[]>(() => readJSON<string[]>(recentKey(), []))
  const [sheetOpen, setSheetOpen] = useState(false)
  // Where the window is short the composer and the keys take turns.
  const [composing, setComposing] = useState(false)

  // A draft belongs to its session: switching away and back finds it again.
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const draft = sessionKey ? drafts[sessionKey] ?? '' : ''
  const setDraft = useCallback((text: string) => {
    if (sessionKey) setDrafts((all) => ({ ...all, [sessionKey]: text }))
  }, [sessionKey])

  // Another kind of session has other pages.
  const [pagesOf, setPagesOf] = useState(kind)
  if (pagesOf !== kind) {
    setPagesOf(kind)
    setPage(0)
  }
  const pages = KEYSETS[kind]
  const keys = pages[page % pages.length]

  // The modifiers in force are told to the session, which applies them to
  // what the on-screen keyboard types and says when it has (lib/terminalKeys).
  const announce = (next: BarModifiers) => window.dispatchEvent(new CustomEvent(MODIFIERS_EVENT, { detail: activeModifiers(next) }))
  const changeMods = (next: BarModifiers) => {
    setMods(next)
    announce(next)
  }
  const modsRef = useRef(mods)
  useEffect(() => { modsRef.current = mods }, [mods])
  useEffect(() => {
    // A typed key used the armed ones up; the locked ones are told again.
    const consumed = () => {
      const next = afterKey(modsRef.current)
      setMods(next)
      window.dispatchEvent(new CustomEvent(MODIFIERS_EVENT, { detail: activeModifiers(next) }))
    }
    window.addEventListener(MODIFIERS_CONSUMED_EVENT, consumed)
    return () => {
      window.removeEventListener(MODIFIERS_CONSUMED_EVENT, consumed)
      window.dispatchEvent(new CustomEvent(MODIFIERS_EVENT, { detail: activeModifiers(NO_BAR_MODIFIERS) }))
    }
  }, [])

  const press = (key: BarKey) => {
    if (key.modifier) {
      changeMods(tapModifier(mods, key.modifier))
      return
    }
    if (key.send === undefined) return
    onSendKey(terminalKey(key.send, activeModifiers(mods)))
    changeMods(afterKey(mods))
  }

  // A held key that repeats, and a sideways drag that turns the page; either
  // one takes the click that would follow it.
  const hold = useRef({ timer: 0, every: 0, fired: false, x: 0, swiped: false })
  const stopHold = () => {
    clearTimeout(hold.current.timer)
    clearInterval(hold.current.every)
    hold.current.timer = hold.current.every = 0
  }
  useEffect(() => stopHold, [])
  // A finger's moves across the keys are taken from the browser. It pans
  // nothing here, but a flick it is not told about it keeps as a fling in
  // progress for a second or two, and the tap that stops a fling is not a
  // click: the key pressed right after turning the page did nothing. React's
  // own touchmove is passive and cannot say so.
  useEffect(() => {
    const row = keysRef.current
    if (!row) return
    const taken = (e: TouchEvent) => { if (e.cancelable) e.preventDefault() }
    row.addEventListener('touchmove', taken, { passive: false })
    return () => row.removeEventListener('touchmove', taken)
  }, [])
  const startHold = (key: BarKey) => {
    stopHold()
    hold.current.fired = false
    if (!key.repeat || key.send === undefined) return
    const send = key.send
    hold.current.timer = window.setTimeout(() => {
      hold.current.fired = true
      // With the modifiers as they stand, and without using the armed ones up
      // at every step: the hold is one key.
      hold.current.every = window.setInterval(() => onSendKey(terminalKey(send, activeModifiers(modsRef.current))), REPEAT_EVERY)
    }, REPEAT_DELAY)
  }

  // The textarea grows with what is written, to five lines.
  useEffect(() => {
    const el = draftRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(132, el.scrollHeight + 2)}px`
  }, [draft, composing])

  const send = () => {
    const text = draft.replace(/[\r\n]+$/, '')
    // Nothing written: the bare Enter an approval prompt waits for.
    if (!text.trim()) {
      onSendKey('\r')
      return
    }
    onSubmit(text, sendEnter)
    const next = [text, ...recent.filter((r) => r !== text)].slice(0, RECENT_MAX)
    setRecent(next)
    writeJSON(recentKey(), next)
    setDraft('')
  }

  const compose = useCallback((text: string) => {
    setDraft(draftRef.current?.value ? `${draftRef.current.value}${text}` : text)
    setComposing(true)
    setSheetOpen(false)
    // After the sheet has let go of the focus.
    setTimeout(() => draftRef.current?.focus(), 0)
  }, [setDraft])
  useEffect(() => {
    const el = rootRef.current as TerminalInputElement | null
    if (!el) return
    el.__compose = compose
    return () => { delete el.__compose }
  }, [compose])

  const keepFocus = (e: React.PointerEvent) => e.preventDefault()
  const pageLabel = `${(page % pages.length) + 1}/${pages.length}`

  return (
    <div ref={rootRef} data-terminal-input data-mobile-terminal-bar className="shrink-0 border-t border-border bg-card pb-safe">
      <div data-terminal-composer className={cn('flex items-end gap-1.5 px-1.5 pt-1.5', !composing && 'short:hidden')}>
        <button type="button" className={ROUND} aria-label={t('terminal.input.tools')} onPointerDown={keepFocus} onClick={() => setSheetOpen(true)}>
          <Plus className="h-5 w-5" aria-hidden="true" />
        </button>
        <textarea
          ref={draftRef}
          rows={1}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            // A hardware keyboard's way to send; plain Enter is a new line.
            if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.nativeEvent.isComposing) { e.preventDefault(); send() }
          }}
          placeholder={kind === 'agent' ? t('terminal.input.placeholderAgent') : t('terminal.input.placeholderShell')}
          aria-label={t('terminal.input.composer')}
          disabled={!sessionKey}
          // A shell's words are not a language's: no capitals, no corrections.
          autoCapitalize={kind === 'shell' ? 'off' : undefined}
          autoCorrect={kind === 'shell' ? 'off' : undefined}
          spellCheck={kind === 'shell' ? false : undefined}
          className="min-h-12 short:min-h-9 max-h-[132px] min-w-0 flex-1 resize-none rounded-xl border border-border bg-background px-3 py-3 short:py-1.5 text-[16px] leading-snug text-foreground outline-none placeholder:text-muted-foreground focus-visible:border-ring disabled:opacity-50"
        />
        <button type="button" className={cn(ROUND, 'hidden short:flex')} aria-label={t('terminal.input.showKeys')} onPointerDown={keepFocus} onClick={() => setComposing(false)}>
          <Keyboard className="h-5 w-5" aria-hidden="true" />
        </button>
        <button type="button" className={cn(ROUND, 'border-primary bg-primary text-primary-foreground active:bg-primary/85')} aria-label={t('terminal.input.send')}
          disabled={!sessionKey} onPointerDown={keepFocus} onClick={send}>
          <ArrowUp className="h-5 w-5" aria-hidden="true" />
        </button>
      </div>

      <div ref={keysRef} role="group" aria-label={t('terminal.mobile.keys')} data-terminal-keys
        className={cn('grid grid-cols-8 short:grid-cols-9 gap-1 px-1.5 py-1.5 touch-none', composing && 'short:hidden')}
        onPointerDown={(e) => { hold.current.x = e.clientX; hold.current.swiped = false }}
        onPointerUp={(e) => {
          stopHold()
          const dx = e.clientX - hold.current.x
          if (Math.abs(dx) < SWIPE) return
          hold.current.swiped = true
          setPage((p) => (p + (dx < 0 ? 1 : pages.length - 1)) % pages.length)
        }}
        onPointerCancel={stopHold}
        onClickCapture={(e) => {
          // The click that ends a hold or a swipe is not a press.
          if (hold.current.fired || hold.current.swiped) { e.stopPropagation(); hold.current.fired = false; hold.current.swiped = false }
        }}>
        {keys.map((key) => {
          const state = key.modifier ? mods[key.modifier] : 0
          const name = key.modifier ? t('terminal.mobile.modifier', { key: key.modifier }) : key.name ? t(`terminal.mobile.${key.name}`) : key.label
          return (
            <button key={key.label} type="button" aria-label={name}
              aria-pressed={key.modifier ? state > 0 : undefined}
              data-locked={state === 2 ? '' : undefined}
              className={cn(KEY, /^[↑↓←→⏎]$/.test(key.label) && 'text-[17px] font-normal', key.danger && 'text-destructive',
                state === 1 && 'border-primary bg-primary/15', state === 2 && 'border-primary bg-primary text-primary-foreground')}
              onPointerDown={(e) => { e.preventDefault(); startHold(key) }}
              onClick={() => press(key)}>
              {key.label}
            </button>
          )
        })}
        <button type="button" className={cn(KEY, 'hidden short:flex items-center justify-center')} aria-label={t('terminal.input.compose')}
          onPointerDown={keepFocus} onClick={() => { setComposing(true); setTimeout(() => draftRef.current?.focus(), 0) }}>
          <PenLine className="h-4 w-4" aria-hidden="true" />
        </button>
        <button type="button" className={cn(KEY, 'text-[11px] text-muted-foreground')} aria-label={t('terminal.input.nextKeys', { page: pageLabel })}
          onPointerDown={keepFocus} onClick={() => setPage((p) => (p + 1) % pages.length)}>
          {pageLabel}
        </button>
      </div>

      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent side="bottom" className="max-h-[75dvh] gap-3 overflow-y-auto rounded-t-2xl p-4">
          <SheetTitle>{t('terminal.input.tools')}</SheetTitle>
          <label className="flex min-h-12 items-center gap-3 text-[14px]">
            <Checkbox checked={sendEnter} onCheckedChange={(v) => { const on = v === true; setSendEnter(on); writeJSON(SEND_ENTER_KEY, on) }} />
            {t('terminal.input.sendEnter')}
          </label>
          <section aria-labelledby="terminal-input-recent" className="flex flex-col gap-1.5">
            <h3 id="terminal-input-recent" className="text-[12px] font-semibold text-muted-foreground">{t('terminal.input.recent')}</h3>
            {recent.length === 0 && <p className="text-[13px] text-muted-foreground">{t('terminal.input.recentEmpty')}</p>}
            {recent.map((text) => (
              <button key={text} type="button" data-recent onClick={() => compose(text)}
                className="min-h-12 rounded-xl border border-border bg-secondary px-3 py-2 text-left text-[14px] line-clamp-3 whitespace-pre-wrap break-words active:bg-accent focus-visible:outline-2 focus-visible:outline-ring">
                {text}
              </button>
            ))}
          </section>
        </SheetContent>
      </Sheet>
    </div>
  )
}
