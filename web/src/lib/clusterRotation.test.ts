import { describe, expect, it } from 'vitest'
import i18next from 'i18next'
import en from '@/i18n/locales/en.json'
import ko from '@/i18n/locales/ko.json'
import { caActive, caCanAbort, caStepper, countdown, pinRole, rotationFailure, shortPin, waitingLine } from './clusterRotation'

const refusal = (status: number, code: string, message: string) =>
  Object.assign(new Error(message), { status, code })

describe('rotationFailure', () => {
  it('reads a gate refusal and a running update as a refusal with its reason', () => {
    expect(rotationFailure(refusal(409, 'CLUSTER_ROTATION_REFUSED', 'waiting for peer-2 (offline)')))
      .toEqual({ refused: true, message: 'waiting for peer-2 (offline)' })
    expect(rotationFailure(refusal(409, 'UPDATE_IN_PROGRESS', 'An update is already running.')).refused).toBe(true)
  })

  it('does not call any other failure a refusal', () => {
    // A leader that could not be confirmed did not refuse: nothing was asked.
    expect(rotationFailure(refusal(503, 'INTERNAL_ERROR', 'no leader'))).toEqual({ refused: false, message: 'no leader' })
    expect(rotationFailure(refusal(409, 'CLUSTER_CA_ROTATING', 'a CA rotation is running')).refused).toBe(false)
    expect(rotationFailure(new Error('Failed to fetch')).refused).toBe(false)
  })
})

describe('caStepper', () => {
  it('walks forward from trust to done', () => {
    expect(caStepper('trust')).toEqual({ branch: 'forward', steps: ['trust', 'reissue', 'promote', 'done'], current: 0 })
    expect(caStepper('promote').current).toBe(2)
    expect(caStepper('done').current).toBe(3)
  })

  it('draws going back as its own branch', () => {
    expect(caStepper('rollback')).toEqual({ branch: 'back', steps: ['rollback', 'drop', 'aborted'], current: 0 })
    expect(caStepper('aborted')).toEqual({ branch: 'back', steps: ['rollback', 'drop', 'aborted'], current: 2 })
  })

  it('marks no step for a phase it does not know', () => {
    expect(caStepper('settle')).toEqual({ branch: 'forward', steps: ['trust', 'reissue', 'promote', 'done'], current: -1 })
  })
})

describe('caCanAbort and caActive', () => {
  it('offers a stop only before promote', () => {
    expect(caCanAbort('trust')).toBe(true)
    expect(caCanAbort('reissue')).toBe(true)
    for (const phase of ['promote', 'done', 'rollback', 'drop', 'aborted', undefined]) expect(caCanAbort(phase)).toBe(false)
  })

  it('holds a new rotation back while one runs, going back included', () => {
    for (const phase of ['trust', 'reissue', 'promote', 'rollback', 'drop']) expect(caActive(phase)).toBe(true)
    for (const phase of ['done', 'aborted', undefined, 'settle']) expect(caActive(phase)).toBe(false)
  })
})

describe('countdown', () => {
  const now = Date.parse('2026-10-29T09:30:00Z')

  it('counts minutes and seconds, and hours past one', () => {
    expect(countdown('2026-10-29T09:51:00Z', now)).toBe('21:00')
    expect(countdown('2026-10-29T09:51:00Z', now + 61_000)).toBe('19:59')
    expect(countdown('2026-10-29T11:35:05Z', now)).toBe('2:05:05')
  })

  it('stops at zero, and says zero for a deadline it cannot read', () => {
    expect(countdown('2026-10-29T09:00:00Z', now)).toBe('00:00')
    expect(countdown('soon', now)).toBe('00:00')
  })
})

describe('pinRole and shortPin', () => {
  const old = `sha256:${'a'.repeat(64)}`
  const next = `sha256:${'b'.repeat(64)}`

  it('names the rotation\'s two CAs and nothing else', () => {
    const rot = { phase: 'reissue', old_pin: old, next_pin: next }
    expect(pinRole(old, rot)).toBe('current')
    expect(pinRole(next, rot)).toBe('next')
    expect(pinRole(`sha256:${'c'.repeat(64)}`, rot)).toBeNull()
    // A node that reported nothing.
    expect(pinRole('', { phase: 'trust', old_pin: '', next_pin: '' })).toBeNull()
  })

  it('calls the next CA current once the rotation is done, and an aborted one\'s no one\'s', () => {
    expect(pinRole(next, { phase: 'done', old_pin: old, next_pin: next })).toBe('current')
    expect(pinRole(old, { phase: 'done', old_pin: old, next_pin: next })).toBe('previous')
    expect(pinRole(old, { phase: 'aborted', old_pin: old, next_pin: next })).toBe('current')
    expect(pinRole(next, { phase: 'aborted', old_pin: old, next_pin: next })).toBeNull()
  })

  it('shortens a pin to its first twelve hex digits', () => {
    expect(shortPin(`sha256:${'0123456789abcdef'.repeat(4)}`)).toBe('0123456789ab…')
    expect(shortPin('sha256:abc')).toBe('abc')
  })
})

describe('waitingLine', () => {
  const i18n = i18next.createInstance()
  void i18n.init({ lng: 'en', resources: { en: { translation: en }, ko: { translation: ko } }, interpolation: { escapeValue: false } })
  const en_ = i18n.getFixedT('en')
  const ko_ = i18n.getFixedT('ko')

  // Every reason the leader writes (agent_register.go, proxy_rotation.go,
  // ca_rotation.go), in both languages.
  it.each([
    ['agent-03 (offline)', 'agent-03: offline', 'agent-03: 오프라인'],
    ['agent-03 (not heard from)', 'agent-03: not heard from', 'agent-03: 응답 없음'],
    ['agent-04 (0.79.3)', 'agent-04: runs 0.79.3, which cannot follow a rotation — update it', 'agent-04: 0.79.3, 교체를 지원하지 않는 버전 — 업데이트하세요'],
    ['agent-04 (0.80.0-rc1)', 'agent-04: runs 0.80.0-rc1, which cannot follow a rotation — update it', 'agent-04: 0.80.0-rc1, 교체를 지원하지 않는 버전 — 업데이트하세요'],
    ['agent-04 (v0.79.4)', 'agent-04: runs v0.79.4, which cannot follow a rotation — update it', 'agent-04: v0.79.4, 교체를 지원하지 않는 버전 — 업데이트하세요'],
    ['agent-05 (not heard from since the rollback phase began)', 'agent-05: not heard from since the rollback phase began', 'agent-05: rollback 단계 시작 뒤 응답 없음'],
    ['manager-02 (has not taken the next secret yet)', 'manager-02: has not taken the next secret yet', 'manager-02: 아직 다음 비밀을 받지 않음'],
    ['agent-01 (does not trust the next CA yet)', 'agent-01: does not trust the next CA yet', 'agent-01: 아직 다음 CA를 신뢰하지 않음'],
    ['agent-01 (still presents a certificate from the current CA)', 'agent-01: still presents a certificate from the current CA', 'agent-01: 아직 현재 CA의 인증서를 씀'],
    ['agent-01 (still trusts the old CA)', 'agent-01: still trusts the old CA', 'agent-01: 아직 이전 CA를 신뢰함'],
    ['agent-01 (still presents a certificate from the next CA)', 'agent-01: still presents a certificate from the next CA', 'agent-01: 아직 다음 CA의 인증서를 씀'],
    ['agent-01 (still trusts the next CA)', 'agent-01: still trusts the next CA', 'agent-01: 아직 다음 CA를 신뢰함'],
  ])('words %s', (line, inEnglish, inKorean) => {
    expect(waitingLine(line, en_)).toBe(inEnglish)
    expect(waitingLine(line, ko_)).toBe(inKorean)
  })

  it('shows any other line as the leader sent it', () => {
    for (const line of [
      'agent-01 (has gone to lunch)',
      'agent-01 ()',
      'agent-01',
      'agent-01 (offline) and more',
      'agent-01 (not heard from since the  phase began)',
    ]) {
      expect(waitingLine(line, en_)).toBe(line)
      expect(waitingLine(line, ko_)).toBe(line)
    }
  })
})
