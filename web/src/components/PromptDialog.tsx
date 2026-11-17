import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

export interface PromptOptions {
  title: string
  description?: string
  placeholder?: string
  defaultValue?: string
  /** Render the input as a password field (masked). */
  password?: boolean
  confirmLabel?: string
  cancelLabel?: string
}

type PromptFn = (opts: PromptOptions) => Promise<string | null>

const PromptContext = createContext<PromptFn | null>(null)

/**
 * usePrompt returns an async prompt(opts) that resolves to the entered string,
 * or null if cancelled — a styled, on-brand replacement for window.prompt.
 *   const v = await prompt({ title: t('...'), password: true }); if (!v) return
 *
 * Co-located with its provider (one module owns the prompt machinery); the only
 * cost is HMR fast-refresh for this file, acknowledged by the disable below.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function usePrompt(): PromptFn {
  const ctx = useContext(PromptContext)
  if (!ctx) throw new Error('usePrompt must be used within <PromptProvider>')
  return ctx
}

export function PromptProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  // As in ConfirmProvider, the options outlive the answer so the dialog keeps
  // its title and buttons while it animates closed; only `open` drops.
  const [opts, setOpts] = useState<PromptOptions | null>(null)
  const [open, setOpen] = useState(false)
  const [value, setValue] = useState('')
  const resolver = useRef<((result: string | null) => void) | null>(null)

  const settle = useCallback((result: string | null) => {
    const resolve = resolver.current
    resolver.current = null
    resolve?.(result)
    setOpen(false)
  }, [])

  const prompt = useCallback<PromptFn>((next) => {
    setValue(next.defaultValue ?? '')
    return new Promise<string | null>((resolve) => {
      // A second prompt replaces one still open, which counts as a cancel.
      resolver.current?.(null)
      resolver.current = resolve
      setOpts(next)
      setOpen(true)
    })
  }, [])

  return (
    <PromptContext.Provider value={prompt}>
      {children}
      <Dialog open={open} onOpenChange={(next) => { if (!next) settle(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{opts?.title}</DialogTitle>
            {opts?.description && <DialogDescription>{opts.description}</DialogDescription>}
          </DialogHeader>
          <form
            onSubmit={(e) => { e.preventDefault(); settle(value) }}
            className="space-y-3"
          >
            {/* Named by the question: most callers pass no placeholder, and
                the field was announced as an unnamed edit box. Every caller
                stops on an empty answer, so an empty one is not submitted. */}
            <Input
              type={opts?.password ? 'password' : 'text'}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder={opts?.placeholder}
              aria-label={opts?.title}
              required
              autoFocus
              autoComplete={opts?.password ? 'current-password' : 'off'}
            />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => settle(null)}>
                {opts?.cancelLabel ?? t('common.cancel')}
              </Button>
              <Button type="submit">
                {opts?.confirmLabel ?? t('common.confirm')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </PromptContext.Provider>
  )
}
