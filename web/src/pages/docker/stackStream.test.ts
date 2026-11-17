import { describe, it, expect } from 'vitest'
import { runStackStream, runEach, type StackStreamEvent } from './stackStream'

// A stream that answers the way the server does: every event, then a normal end.
const stream = (events: StackStreamEvent[]) =>
  async (onEvent: (event: StackStreamEvent) => void) => { for (const e of events) onEvent(e) }

describe('runStackStream', () => {
  it('rejects with the error line when the stream reported a failure and then ended normally', async () => {
    const seen: string[] = []
    const run = runStackStream(
      stream([
        { phase: 'pull', line: 'pulling web' },
        { phase: 'error', line: 'Bind for 0.0.0.0:80 failed: port is already allocated' },
      ]),
      (e) => seen.push(e.line),
    )
    await expect(run).rejects.toThrow('Bind for 0.0.0.0:80 failed: port is already allocated')
    // The dialog still gets every line, the failure included.
    expect(seen).toEqual(['pulling web', 'Bind for 0.0.0.0:80 failed: port is already allocated'])
  })

  it('reports the last failure when there were several', async () => {
    await expect(runStackStream(stream([
      { phase: 'error', line: 'first' },
      { phase: 'error', line: 'second' },
    ]), () => {})).rejects.toThrow('second')
  })

  it('resolves when the stream completed', async () => {
    await expect(runStackStream(stream([
      { phase: 'up', line: 'starting web' },
      { phase: 'complete', line: 'Deployment completed' },
    ]), () => {})).resolves.toBeUndefined()
  })

  it('passes a request failure through unchanged', async () => {
    await expect(runStackStream(async () => { throw new Error('Deploy failed') }, () => {}))
      .rejects.toThrow('Deploy failed')
  })
})

describe('runEach', () => {
  it('goes on past a failure and returns the items that failed', async () => {
    const done: string[] = []
    const failed = await runEach(['a', 'b', 'c'], async (id) => {
      if (id === 'b') throw new Error('no such container')
      done.push(id)
    })
    expect(done).toEqual(['a', 'c'])
    expect(failed).toEqual(['b'])
  })

  it('returns nothing when every item succeeded', async () => {
    expect(await runEach(['a', 'b'], async () => {})).toEqual([])
  })
})
