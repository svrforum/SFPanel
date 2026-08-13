import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertCircle, CheckCircle2, Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

export interface SSEOutputState {
  open: boolean
  title: string
  output: string
  done: boolean
  error?: boolean
}

const CLOSED: SSEOutputState = { open: false, title: '', output: '', done: false }

// SSE installers emit "ERROR: …" lines then [DONE] even on failure, so the
// "done" flag alone can't tell success from failure. Scan the streamed output
// for an error marker to render the right state.
function outputHasError(output: string): boolean {
  return /(^|\n)\s*(ERROR:|error:|E:\s|fatal:)/m.test(output)
}

// api.postTextStream throws technical, untranslated errors ("Stream failed
// (500)", "No stream"). Map those to a translated fallback; anything else
// (backend-provided messages) passes through.
// eslint-disable-next-line react-refresh/only-export-components
export function streamErrorMessage(err: unknown, fallback: string): string {
  if (
    err instanceof Error &&
    err.message &&
    !err.message.startsWith('Stream failed') &&
    err.message !== 'No stream'
  ) {
    return err.message
  }
  return fallback
}

export interface SSEOutput {
  state: SSEOutputState
  openOutput: (title: string) => void
  appendOutput: (text: string) => void
  finishOutput: () => void
  closeOutput: () => void
  runStream: (path: string, opts?: { body?: unknown; finishOnDone?: boolean }) => Promise<void>
}

/**
 * Streaming-output dialog state machine for long-running command surfaces
 * (Packages install/upgrade streams; adoptable by NetworkTailscale). Pair
 * with <OutputDialog state={output.state} onClose={output.closeOutput} />.
 *
 * Co-located with its dialog on purpose (one module owns the output-dialog
 * machinery), same trade-off as ConfirmDialog — the disables above/below
 * acknowledge the HMR fast-refresh cost.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function useSSEOutput(): SSEOutput {
  const [state, setState] = useState<SSEOutputState>(CLOSED)

  const openOutput = useCallback((title: string) => {
    setState({ open: true, title, output: '', done: false })
  }, [])

  const appendOutput = useCallback((text: string) => {
    setState((prev) => ({ ...prev, output: prev.output + text }))
  }, [])

  const finishOutput = useCallback(() => {
    setState((prev) => ({ ...prev, done: true, error: outputHasError(prev.output) }))
  }, [])

  const closeOutput = useCallback(() => {
    setState(CLOSED)
  }, [])

  // POST an SSE text-stream endpoint via api.postTextStream (which applies the
  // cluster ?node= scope) and pipe its `data:` lines into the dialog. `[DONE]`
  // finishes the dialog unless finishOnDone=false — callers that append their
  // own closing lines finish explicitly.
  const runStream = useCallback(
    async (path: string, opts: { body?: unknown; finishOnDone?: boolean } = {}) => {
      let buffer = ''
      await api.postTextStream(
        path,
        (chunk) => {
          buffer += chunk
          const lines = buffer.split('\n')
          buffer = lines.pop() || ''
          for (const line of lines) {
            if (!line.startsWith('data: ')) continue
            const data = line.slice(6)
            if (data === '[DONE]') {
              if (opts.finishOnDone !== false) finishOutput()
            } else {
              appendOutput(data + '\n')
            }
          }
        },
        opts.body !== undefined
          ? { body: JSON.stringify(opts.body), headers: { 'Content-Type': 'application/json' } }
          : {}
      )
    },
    [appendOutput, finishOutput]
  )

  return { state, openOutput, appendOutput, finishOutput, closeOutput, runStream }
}

export function OutputDialog({ state, onClose }: { state: SSEOutputState; onClose: () => void }) {
  const { t } = useTranslation()
  const outputRef = useRef<HTMLDivElement>(null)

  // Auto-scroll output to bottom as lines stream in
  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight
    }
  }, [state.output])

  return (
    <Dialog
      open={state.open}
      onOpenChange={(open) => {
        if (!open && state.done) onClose()
      }}
    >
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {!state.done && <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />}
            {state.done && state.error && (
              <AlertCircle className="h-4 w-4 text-destructive" aria-hidden="true" />
            )}
            {state.done && !state.error && (
              <CheckCircle2 className="h-4 w-4 text-success" aria-hidden="true" />
            )}
            {state.title}
          </DialogTitle>
          <DialogDescription>
            {!state.done
              ? t('common.output.operationRunning')
              : state.error
                ? t('common.output.operationFailed')
                : t('common.output.operationComplete')}
          </DialogDescription>
        </DialogHeader>
        <div ref={outputRef} className="bg-zinc-950 text-zinc-100 rounded-xl p-4 max-h-96 overflow-y-auto">
          <pre className="text-xs font-mono whitespace-pre-wrap break-words">
            {state.output || t('common.output.waitingForOutput')}
          </pre>
        </div>
        <DialogFooter>
          <Button variant="outline" className="rounded-xl" onClick={onClose} disabled={!state.done}>
            {state.done ? t('common.close') : t('common.output.pleaseWait')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
