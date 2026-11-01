import { describe, expect, it } from 'vitest'
import type { TFunction } from 'i18next'
import { taskErrorMessage } from './taskErrors'

// A translator that answers with the key, so which sentence was chosen shows.
const t = ((key: string) => key) as unknown as TFunction

const refusal = (status: number, message: string, code?: string) => Object.assign(new Error(message), { status, code })

describe('taskErrorMessage', () => {
  // The task routes answer 503 INTERNAL_ERROR while the leader changes or
  // catches up, in one of three English sentences. Each means the same to
  // the operator: nothing was written, try again shortly.
  it('reads any 503 as the leader being busy', () => {
    expect(taskErrorMessage(refusal(503, 'The cluster leader is catching up: retry in a few seconds', 'INTERNAL_ERROR'), t))
      .toBe('cluster.tasks.errors.leaderBusy')
    expect(taskErrorMessage(refusal(503, 'No cluster leader available — retry in a few seconds', 'INTERNAL_ERROR'), t))
      .toBe('cluster.tasks.errors.leaderBusy')
  })

  // A body over 1 MiB is refused by Echo before the handler, with no
  // envelope: the client knows only "HTTP 413".
  it('reads a 413 that carries no code as a request too large', () => {
    expect(taskErrorMessage(refusal(413, 'HTTP 413'), t)).toBe('cluster.tasks.errors.tooLarge')
    // One that does carry a code says its own.
    expect(taskErrorMessage(refusal(413, 'Worded by its code.', 'SOME_CODE'), t)).toBe('Worded by its code.')
  })

  // Any refusal with a code was already worded by CODE_MESSAGES on its way
  // out of the client, details filled in.
  it('keeps the message of a refusal with a code', () => {
    expect(taskErrorMessage(refusal(400, 'Too large: the limit is 64 KiB.', 'TASK_TOO_LARGE'), t)).toBe('Too large: the limit is 64 KiB.')
    expect(taskErrorMessage(refusal(409, 'Run r1 is under way.', 'TASK_RUN_ACTIVE'), t)).toBe('Run r1 is under way.')
    expect(taskErrorMessage(refusal(502, 'Proxy request failed: EOF', 'INTERNAL_ERROR'), t)).toBe('Proxy request failed: EOF')
  })

  it('says what was thrown when it is not an error', () => {
    expect(taskErrorMessage('boom', t)).toBe('boom')
  })
})
