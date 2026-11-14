import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUp, BookmarkPlus, Keyboard, Loader2, Paperclip, PenLine, Plus, Settings2, X } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { MODIFIERS_CONSUMED_EVENT, MODIFIERS_EVENT, terminalKey } from '@/lib/terminalKeys'
import { LONG_LABEL, NO_BAR_MODIFIERS, activeModifiers, afterKey, barKeyName, keysetPages, longLabel, tapModifier, type BarKey, type BarModifiers, type KeysetKind, type StoredKeysets } from '@/lib/terminalKeysets'
import { attachedText, carriedFiles } from '@/lib/terminalAttach'
import { snippetBody, snippetRefusal } from '@/lib/terminalPrefs'
import { useTerminalPrefs } from '@/hooks/useTerminalPrefs'
import { KeysEditor } from '@/pages/terminal/components/KeysEditor'
import type { TerminalSnippet } from '@/types/api'

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

const KEY = 'min-w-0 min-h-12 short:min-h-9 overflow-hidden rounded-lg border border-border bg-secondary text-[13px] font-semibold text-foreground select-none active:bg-accent focus-visible:outline-2 focus-visible:outline-ring'
const ROW = 'min-h-12 min-w-0 flex-1 rounded-xl border border-border bg-secondary px-3 py-2 text-left text-[14px] line-clamp-3 whitespace-pre-wrap break-words active:bg-accent focus-visible:outline-2 focus-visible:outline-ring'
const ROW_ACTION = 'flex min-h-12 w-11 shrink-0 items-center justify-center rounded-xl border border-border bg-secondary text-muted-foreground active:bg-accent focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-40'
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
 * session runs (lib/terminalKeysets) — the panel's own until the operator
 * arranges theirs. A press never takes the focus: the keyboard stays with the
 * composer or the terminal, whichever had it.
 *
 * The + sheet holds what goes with writing: attaching a file, the snippets
 * the operator keeps, what was sent lately, and the key editor. Snippets and
 * key rows are kept by the panel (lib/terminalPrefs), so a phone and a desk
 * have the same ones.
 *
 * `data-mobile-terminal-bar` is what Android apps up to 0.1.6 hide — they draw
 * key rows of their own — so they keep working unchanged; 0.1.7 leaves this
 * one in place and draws none.
 */
export function TerminalInput({ kind, sessionKey, onSendKey, onSubmit, onAttach }: {
  kind: KeysetKind
  /** whose draft the composer holds; null with no session open */
  sessionKey: string | null
  onSendKey: (data: string) => void
  /** the composed text, and whether the Enter that submits it follows */
  onSubmit: (text: string, enter: boolean) => void
  /** uploads a file to the session's node and resolves to its path there;
   *  absent where the session cannot take one (a temporary shell, an older panel) */
  onAttach?: (file: File) => Promise<string>
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
  // The key rows the editor starts from, while it is open.
  const [editingKeys, setEditingKeys] = useState<StoredKeysets | null>(null)
  const { prefs, status: prefsStatus, load: loadPrefs, changeSnippets, loadKeysets, saveKeysets } = useTerminalPrefs()
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
  const pages = keysetPages(kind, prefs.keysets)
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
    // A text of the operator's own goes as the composer's does — a paste, and
    // the Enter after it when the key says so. One character alone is a key
    // like any other, and takes the modifiers.
    if (key.own && (key.enter || key.send.length > 1)) onSubmit(key.send, key.enter === true)
    else onSendKey(terminalKey(key.send, activeModifiers(mods)))
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

  // Text put into the draft brings the keyboard to the composer — when the
  // composer can take it, which a timer cannot know. The commit that shows the
  // composer and closes the sheet comes whenever React gets to it (at once
  // from a tap, later from the end of an upload or a call from the app), and a
  // sheet that is still up pulls back a focus given before it has let go. So:
  // after that commit (the effect below), and if a sheet is up, when its
  // content says it has let go (onCloseAutoFocus).
  const focusAsked = useRef(false)
  const sheetUp = useRef(false)
  const [focusAsks, setFocusAsks] = useState(0)
  useEffect(() => {
    if (!focusAsked.current || sheetUp.current) return
    focusAsked.current = false
    draftRef.current?.focus()
  }, [focusAsks])
  const compose = useCallback((text: string) => {
    setDraft(draftRef.current?.value ? `${draftRef.current.value}${text}` : text)
    setComposing(true)
    setSheetOpen(false)
    focusAsked.current = true
    setFocusAsks((n) => n + 1)
  }, [setDraft])
  useEffect(() => {
    const el = rootRef.current as TerminalInputElement | null
    if (!el) return
    el.__compose = compose
    return () => { delete el.__compose }
  }, [compose])

  // Attachments: each file goes up to the session's node, and its path there
  // goes into the draft — what a prompt points an AI CLI at. From the sheet's
  // picker, or pasted into the composer (a screenshot from the clipboard).
  const fileRef = useRef<HTMLInputElement>(null)
  const [attaching, setAttaching] = useState(0)
  const sessionKeyRef = useRef(sessionKey)
  useEffect(() => { sessionKeyRef.current = sessionKey }, [sessionKey])
  const attach = async (files: File[]) => {
    // The session the files go to is the one on screen now; an upload takes a
    // moment, and its path belongs in that session's draft whichever session
    // is on screen when it is done.
    const key = sessionKey
    if (!onAttach || !key || files.length === 0) return
    setAttaching((n) => n + files.length)
    const paths: string[] = []
    for (const file of files) {
      try {
        paths.push(await onAttach(file))
      } catch (err: unknown) {
        const tooLarge = (err as { code?: string }).code === 'FILE_TOO_LARGE'
        toast.error(t(tooLarge ? 'terminal.input.attachTooLarge' : 'terminal.input.attachFailed', { name: file.name || t('terminal.input.attachUnnamed') }))
      } finally {
        setAttaching((n) => n - 1)
      }
    }
    if (paths.length === 0) return
    if (key === sessionKeyRef.current) compose(attachedText(paths, draftRef.current?.value ?? ''))
    else setDrafts((all) => ({ ...all, [key]: `${all[key] ?? ''}${attachedText(paths, all[key] ?? '')}` }))
  }

  // Snippets: what the operator keeps to send again, for this kind of
  // session, newest first. Each change is made to the list the panel holds
  // (hooks/useTerminalPrefs), not to the one on screen.
  const snippets = prefs.snippets.filter((s) => s.kind === kind)
  const same = (a: TerminalSnippet, b: TerminalSnippet) => a.kind === b.kind && a.body === b.body
  // The last one removed and where it was, for the way back. It is offered in
  // the sheet itself: a toast's button cannot be pressed while a sheet is
  // open — everything outside it is inert.
  const [removed, setRemoved] = useState<{ snippet: TerminalSnippet; index: number } | null>(null)
  const keepSnippet = async (text: string) => {
    const snippet: TerminalSnippet = { kind, body: snippetBody(text) }
    if (!snippet.body) return
    let refusal: ReturnType<typeof snippetRefusal> = null
    const result = await changeSnippets((list) => {
      refusal = snippetRefusal(list, snippet)
      return refusal ? null : [snippet, ...list]
    })
    if (result === 'saved') {
      setRemoved(null)
      toast.success(t('terminal.input.snippetSaved'))
    } else if (result === 'failed') toast.error(t('terminal.input.saveFailed'))
    else if (refusal === 'there') toast(t('terminal.input.snippetThere'))
    else if (refusal === 'long') toast.error(t('terminal.input.snippetTooLong'))
    else if (refusal === 'full') toast.error(t('terminal.input.snippetsFull'))
  }
  const dropSnippet = async (snippet: TerminalSnippet) => {
    let index = -1
    const result = await changeSnippets((list) => {
      index = list.findIndex((s) => same(s, snippet))
      return index < 0 ? null : list.filter((_, i) => i !== index)
    })
    // A long prompt is not something to lose to a slip of the thumb.
    if (result === 'saved') setRemoved({ snippet, index })
    else if (result === 'failed') toast.error(t('terminal.input.saveFailed'))
  }
  const restoreSnippet = async () => {
    if (!removed) return
    const { snippet, index } = removed
    const result = await changeSnippets((list) => (list.some((s) => same(s, snippet)) ? null : [...list.slice(0, index), snippet, ...list.slice(index)]))
    if (result === 'failed') toast.error(t('terminal.input.saveFailed'))
    else setRemoved(null)
  }
  // The editor opens on the key rows the panel holds, and not at all while it
  // cannot be asked: what it would show then are not the operator's keys, and
  // saving them would replace the ones that are.
  const openKeysEditor = async () => {
    const held = await loadKeysets()
    if (held === null) {
      toast.error(t('terminal.input.loadFailed'))
      return
    }
    setSheetOpen(false)
    setEditingKeys(held)
  }

  const keepFocus = (e: React.PointerEvent) => e.preventDefault()
  const pageLabel = `${(page % pages.length) + 1}/${pages.length}`

  return (
    <div ref={rootRef} data-terminal-input data-mobile-terminal-bar className="shrink-0 border-t border-border bg-card pb-safe">
      <div data-terminal-composer className={cn('flex items-end gap-1.5 px-1.5 pt-1.5', !composing && 'short:hidden')}>
        <button type="button" className={ROUND} aria-label={t('terminal.input.tools')} onPointerDown={keepFocus} onClick={() => { sheetUp.current = true; setSheetOpen(true) }}>
          <Plus className="h-5 w-5" aria-hidden="true" />
        </button>
        <textarea
          ref={draftRef}
          rows={1}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onPaste={(e) => {
            // A pasted image is a file, not text: it is attached. Text pastes as it does anywhere.
            const files = carriedFiles(e.clipboardData)
            if (!onAttach || files.length === 0) return
            e.preventDefault()
            void attach(files)
          }}
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
        {keys.map((key, i) => {
          const state = key.modifier ? mods[key.modifier] : 0
          return (
            <button key={`${page}:${i}`} type="button" aria-label={barKeyName(key, t)}
              aria-pressed={key.modifier ? state > 0 : undefined}
              data-locked={state === 2 ? '' : undefined}
              className={cn(KEY, /^[↑↓←→⏎]$/.test(key.label) && 'text-[17px] font-normal', longLabel(key.label) && LONG_LABEL, key.danger && 'text-destructive',
                state === 1 && 'border-primary bg-primary/15', state === 2 && 'border-primary bg-primary text-primary-foreground')}
              onPointerDown={(e) => { e.preventDefault(); startHold(key) }}
              onClick={() => press(key)}>
              {key.label}
            </button>
          )
        })}
        {/* A page of fewer keys keeps the cells that turn it where they always are. */}
        {keys.length < 7 && <span aria-hidden="true" style={{ gridColumn: `span ${7 - keys.length}` }} />}
        <button type="button" className={cn(KEY, 'hidden short:flex items-center justify-center')} aria-label={t('terminal.input.compose')}
          onPointerDown={keepFocus} onClick={() => { setComposing(true); setTimeout(() => draftRef.current?.focus(), 0) }}>
          <PenLine className="h-4 w-4" aria-hidden="true" />
        </button>
        <button type="button" className={cn(KEY, 'text-[11px] text-muted-foreground')} aria-label={t('terminal.input.nextKeys', { page: pageLabel })}
          onPointerDown={keepFocus} onClick={() => setPage((p) => (p + 1) % pages.length)}>
          {pageLabel}
        </button>
      </div>

      <Sheet open={sheetOpen} onOpenChange={(open) => {
        setSheetOpen(open)
        // Opened before the panel has answered: it is asked again.
        if (open && prefsStatus !== 'ready') void loadPrefs()
        if (!open) setRemoved(null)
      }}>
        <SheetContent side="bottom" className="max-h-[75dvh] gap-3 overflow-y-auto rounded-t-2xl p-4"
          onCloseAutoFocus={(e) => {
            // The sheet has gone and is about to hand the focus back to where
            // it was; text put into the draft sends it to the composer instead.
            sheetUp.current = false
            if (!focusAsked.current) return
            focusAsked.current = false
            e.preventDefault()
            draftRef.current?.focus()
          }}>
          <SheetTitle>{t('terminal.input.tools')}</SheetTitle>
          <label className="flex min-h-12 items-center gap-3 text-[14px]">
            <Checkbox checked={sendEnter} onCheckedChange={(v) => { const on = v === true; setSendEnter(on); writeJSON(SEND_ENTER_KEY, on) }} />
            {t('terminal.input.sendEnter')}
          </label>
          {onAttach && (
            <>
              <button type="button" disabled={attaching > 0} onClick={() => fileRef.current?.click()}
                className="flex min-h-12 items-center gap-3 rounded-xl border border-border bg-secondary px-3 text-left text-[14px] active:bg-accent focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-60">
                {attaching > 0 ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" /> : <Paperclip className="h-4 w-4" aria-hidden="true" />}
                <span className="flex flex-col">
                  {attaching > 0 ? t('terminal.input.attaching', { n: attaching }) : t('terminal.input.attach')}
                  <span className="text-[12px] text-muted-foreground">{t('terminal.input.attachHint')}</span>
                </span>
              </button>
              <input ref={fileRef} type="file" multiple hidden data-terminal-attach
                onChange={(e) => { const files = Array.from(e.target.files ?? []); e.target.value = ''; void attach(files) }} />
            </>
          )}
          <section aria-labelledby="terminal-input-snippets" className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between gap-2">
              <h3 id="terminal-input-snippets" className="text-[12px] font-semibold text-muted-foreground">{t('terminal.input.snippets')}</h3>
              <button type="button" disabled={!snippetBody(draft)} onClick={() => void keepSnippet(draft)}
                className="flex min-h-9 items-center gap-1 rounded-lg px-2 text-[13px] font-medium text-primary active:bg-accent focus-visible:outline-2 focus-visible:outline-ring disabled:text-muted-foreground disabled:opacity-60">
                <BookmarkPlus className="h-4 w-4" aria-hidden="true" />{t('terminal.input.snippetSave')}
              </button>
            </div>
            {removed && (
              <p role="status" className="flex min-h-11 items-center justify-between gap-2 rounded-xl bg-accent px-3 text-[13px]">
                {t('terminal.input.snippetRemoved')}
                <button type="button" onClick={() => void restoreSnippet()}
                  className="min-h-9 rounded-lg px-2 font-medium text-primary active:bg-background focus-visible:outline-2 focus-visible:outline-ring">
                  {t('terminal.input.undo')}
                </button>
              </p>
            )}
            {prefsStatus === 'failed' && <p role="alert" className="text-[13px] text-destructive">{t('terminal.input.loadFailed')}</p>}
            {prefsStatus === 'ready' && snippets.length === 0 && !removed && <p className="text-[13px] text-muted-foreground">{t('terminal.input.snippetsEmpty')}</p>}
            {snippets.map((snippet) => (
              <div key={snippet.body} className="flex items-stretch gap-1">
                <button type="button" data-snippet onClick={() => compose(snippet.body)} className={ROW}>{snippet.body}</button>
                <button type="button" aria-label={t('terminal.input.snippetRemove')} onClick={() => void dropSnippet(snippet)} className={ROW_ACTION}>
                  <X className="h-4 w-4" aria-hidden="true" />
                </button>
              </div>
            ))}
          </section>
          <section aria-labelledby="terminal-input-recent" className="flex flex-col gap-1.5">
            <h3 id="terminal-input-recent" className="text-[12px] font-semibold text-muted-foreground">{t('terminal.input.recent')}</h3>
            {recent.length === 0 && <p className="text-[13px] text-muted-foreground">{t('terminal.input.recentEmpty')}</p>}
            {recent.map((text) => (
              <div key={text} className="flex items-stretch gap-1">
                <button type="button" data-recent onClick={() => compose(text)} className={ROW}>{text}</button>
                <button type="button" aria-label={t('terminal.input.snippetSaveThis')} onClick={() => void keepSnippet(text)} className={ROW_ACTION}
                  disabled={snippets.some((s) => s.body === snippetBody(text))}>
                  <BookmarkPlus className="h-4 w-4" aria-hidden="true" />
                </button>
              </div>
            ))}
          </section>
          <button type="button" onClick={() => void openKeysEditor()}
            className="flex min-h-12 items-center gap-3 rounded-xl border border-border bg-secondary px-3 text-left text-[14px] active:bg-accent focus-visible:outline-2 focus-visible:outline-ring">
            <Settings2 className="h-4 w-4" aria-hidden="true" />
            <span className="flex flex-col">
              {t('terminal.input.editKeys')}
              <span className="text-[12px] text-muted-foreground">{t('terminal.input.editKeysHint')}</span>
            </span>
          </button>
        </SheetContent>
      </Sheet>
      {editingKeys && <KeysEditor kind={kind} saved={editingKeys} onSave={saveKeysets} onClose={() => setEditingKeys(null)} />}
    </div>
  )
}
