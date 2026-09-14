import { describe, expect, it } from 'vitest'
import type { AISession } from '@/types/api'
import { defaultTitle, stateDotClass, titlePrefix, waitingCount } from './aiSessions'

const s = (id: string, state: AISession['state']): AISession => ({
  id, tool: 'claude', title: id, run_as: 'root', cwd: '/', state, persistence: 'scope', attached: false, created_at: '',
})

describe('aiSessions helpers', () => {
  it('never pulses the tab the operator is looking at', () => {
    expect(stateDotClass('waiting', false)).toContain('animate-pulse')
    expect(stateDotClass('waiting', true)).not.toContain('animate-pulse')
    expect(stateDotClass('working', false)).toBe('bg-success')
  })

  it('counts waiting sessions other than the active one', () => {
    const list = [s('a', 'waiting'), s('b', 'waiting'), s('c', 'working'), s('d', 'ended')]
    expect(waitingCount(list, 'a')).toBe(1)
    expect(waitingCount(list, null)).toBe(2)
  })

  it('titles a session after its tool and directory', () => {
    expect(defaultTitle('claude', '/opt/stacks/myapp')).toBe('Claude · myapp')
    expect(defaultTitle('shell', '/')).toBe('Shell · /')
    expect(defaultTitle('codex', '/home/alice/')).toBe('Codex · alice')
  })

  it('prefixes the document title only when something waits', () => {
    expect(titlePrefix(0)).toBe('')
    expect(titlePrefix(3)).toBe('(3) ')
  })
})
