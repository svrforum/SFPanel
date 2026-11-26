import { afterEach, describe, expect, it, vi } from 'vitest'
import i18next from 'i18next'
import ko from '@/i18n/locales/ko.json'
import en from '@/i18n/locales/en.json'
import { CODE_DEFAULTS, CODE_MESSAGES } from './codeMessages'
import { warningText } from './warnings'
import { installReasonText } from '@/pages/cluster/components/tasks/install/installRun'

// That every code's message exists in both languages is checked in
// i18n/locales.test.ts. This checks what the message asks to be filled in.

// The details each code is sent with, as the Go call sites name them:
// response.FailWith and RepoWarnings, the refusal and fail helpers of
// internal/feature/tasks and internal/feature/agentinstall (an install row's
// reason_details), and githubapi's classification. A placeholder that is
// neither here nor in CODE_DEFAULTS is one the server never fills, and the
// operator reads "{{name}}".
const SERVER_DETAILS: Record<string, string[]> = {
  SERVICE_PROTECTED: ['unit', 'hint'],
  SERVICE_CONFIRM_REQUIRED: ['unit'],
  PROCESS_PROTECTED: ['name'],
  PROCESS_IS_GUEST: ['vmid'],
  DISK_GUEST_VOLUME: ['device', 'owner'],
  DISK_BOOT_PARTITION: ['device'],
  LV_HAS_DEPENDENTS: ['lv'],
  LV_SHRINK_CONFIRM_REQUIRED: ['lv'],
  SWAP_UNSUPPORTED_FS: ['fs'],
  WG_RESOLVCONF_MISSING: ['iface'],
  TAILSCALE_ROUTE_OVERLAP: ['peer', 'prefix'],
  APT_REPO_UNAUTHORIZED: ['uri'],
  APT_REPO_FAILED: ['uri'],
  ALERT_RULE_TYPE_NOT_EVALUATED: ['type'],
  CLUSTER_NODE_UNREACHABLE: ['name'],
  CLUSTER_LEAVE_UNACKNOWLEDGED: ['node_id'],
  CLUSTER_AGENTS_NOT_REACHED: ['agents'],
  TASK_TOO_LARGE: ['max_kib', 'input'],
  TASK_MANAGERS_NOT_READY: ['managers'],
  TASK_INPUT_MISSING: ['input'],
  TASK_TOO_MANY_TARGETS: ['max'],
  TASK_REBOOT_BUSY: ['run_id'],
  TASK_RUN_ACTIVE: ['run_id'],
  AGENT_INSTALL_INVALID: ['field', 'row', 'max', 'same_as'],
  AGENT_INSTALL_HOST_KEY_CHANGED: ['approved', 'host_key'],
  AGENT_INSTALL_UNRELEASED: ['version'],
  AGENT_INSTALL_MIXED_VERSIONS: ['version', 'managers'],
  AGENT_INSTALL_LOGIN_LIMIT: ['max'],
  AGENT_INSTALL_UNREACHABLE: ['address', 'host'],
  AGENT_INSTALL_NOT_ROOT: ['sudo'],
  AGENT_INSTALL_OS: ['os_id', 'os_like'],
  AGENT_INSTALL_ARCH: ['arch'],
  AGENT_INSTALL_NO_TOOLS: ['missing'],
  AGENT_INSTALL_DISK: ['free_kb', 'need_kb'],
  AGENT_INSTALL_PORT_BUSY: ['port', 'owner'],
  AGENT_INSTALL_CLOCK: ['skew_seconds'],
  AGENT_INSTALL_CANNOT_REACH_MANAGER: ['manager'],
  AGENT_INSTALL_IP_NOT_LOCAL: ['ip'],
  AGENT_INSTALL_MANAGER_FILES: ['files'],
  AGENT_INSTALL_NEWER_INSTALLED: ['installed', 'version'],
  AGENT_INSTALL_NAME_INVALID: ['name', 'hostname'],
  AGENT_INSTALL_CLUSTER_NOT_READY: ['gate'],
  AGENT_INSTALL_IDENTITY_TAKEN: ['name', 'ip', 'row'],
  AGENT_INSTALL_LABEL_FAILED: ['group'],
  AGENT_INSTALL_LOGIN_OFF_NETWORK: ['address', 'tailnet'],
  GIT_CLONE_FAILED: ['cause'],
  GIT_RATE_LIMITED: ['reset_at', 'limit', 'retry_after'],
}

// Filled by the screen that words the code rather than by the server: the
// address of an install row, which installRun.ts takes from the row.
const SCREEN_DETAILS: Record<string, string[]> = {
  AGENT_INSTALL_LEADER_CHANGED: ['ip'],
}

function leaf(tree: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>(
    (node, part) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined),
    tree,
  )
}

// i18next resolves a count through key_one / key_other.
function placeholders(tree: unknown, key: string): string[] {
  return [key, `${key}_one`, `${key}_other`].flatMap((k) => {
    const s = leaf(tree, k)
    return typeof s === 'string' ? [...s.matchAll(/\{\{\s*([A-Za-z_]\w*)\s*\}\}/g)].map((m) => m[1]) : []
  })
}

describe('error-code messages', () => {
  it.each([['ko', ko], ['en', en]] as const)('fill every placeholder from the details or a default in %s', (_lang, tree) => {
    const unfilled = Object.entries(CODE_MESSAGES).flatMap(([code, key]) => {
      const known = new Set([
        'serverMessage', // the server's own sentence, which codeText always supplies
        ...Object.keys(CODE_DEFAULTS[code] ?? {}),
        ...(SERVER_DETAILS[code] ?? []),
        ...(SCREEN_DETAILS[code] ?? []),
      ])
      return placeholders(tree, key).filter((name) => !known.has(name)).map((name) => `${code} → {{${name}}}`)
    })
    expect(unfilled).toEqual([])
  })

  // apiErrors holds the older codes' words and nothing else.
  it.each([['ko', ko], ['en', en]] as const)('keep in apiErrors only what a code is worded by, in %s', (_lang, tree) => {
    const worded = Object.values(CODE_MESSAGES)
      .filter((key) => key.startsWith('apiErrors.'))
      .map((key) => key.slice('apiErrors.'.length))
      .sort()
    expect(Object.keys(tree.apiErrors).sort()).toEqual(worded)
  })
})

// The client reads storage in its constructor and runs in node with no DOM
// (same harness as apiError.test.ts).
function memoryStorage(): Storage {
  const map = new Map<string, string>()
  return {
    get length() {
      return map.size
    },
    clear: () => map.clear(),
    getItem: (key: string) => map.get(key) ?? null,
    key: (index: number) => Array.from(map.keys())[index] ?? null,
    removeItem: (key: string) => {
      map.delete(key)
    },
    setItem: (key: string, value: string) => {
      map.set(key, value)
    },
  }
}

async function koreanApi() {
  vi.stubGlobal('sessionStorage', memoryStorage())
  vi.stubGlobal('localStorage', memoryStorage())
  vi.stubGlobal('window', {})
  vi.stubGlobal('document', { cookie: '' })
  vi.resetModules()
  const client = await import('./api')
  const i18n = i18next.createInstance()
  void i18n.init({ lng: 'ko', resources: { en: { translation: en }, ko: { translation: ko } }, interpolation: { escapeValue: false } })
  // As i18n/index.ts hands the client its translator.
  client.setApiTranslator((key, fallback, params) => i18n.t(key, { ...params, defaultValue: fallback }))
  return client
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('apiErrorFrom, on a Korean screen', () => {
  const refusal = (api: Awaited<ReturnType<typeof koreanApi>>, status: number, code: string, message: string) =>
    api.apiErrorFrom(status, { success: false, error: { code, message } })

  // The review saw this one in English on a Korean screen.
  it('words a code whose sentence is the same at every call', async () => {
    const api = await koreanApi()
    expect(refusal(api, 409, 'DESTINATION_EXISTS', "'notes.txt' already exists").message).toBe(ko.apiErrors.destinationExists)
  })

  // Which tool is missing, and the package that brings it, are only in the
  // server's sentence: the wording says what happened and keeps that beside it.
  it('keeps the tool and its package when a tool is not installed', async () => {
    const api = await koreanApi()
    const sent = 'LVM tools are not installed. Install lvm2: apt install lvm2'
    const err = refusal(api, 503, 'TOOL_NOT_INSTALLED', sent)
    expect(err.message).toBe(`이 작업에 필요한 도구가 이 서버에 설치되어 있지 않습니다. (${sent})`)
    expect(err.serverMessage).toBe(sent)
  })

  // Why Proxmox could not list the guests is in pvesh's own output only.
  it('keeps pvesh\'s output when the guests could not be listed', async () => {
    const api = await koreanApi()
    const sent = 'pvesh failed: ipcc_send_rec[1] failed: Connection refused'
    const err = refusal(api, 500, 'PVE_GUESTS_ERROR', sent)
    expect(err.message).toBe(`Proxmox VE에서 이 서버의 가상 머신과 LXC 목록을 읽지 못했습니다. (${sent})`)
  })

  // Worded by its code, the sentence would lose the one thing it says: the
  // command's own output, or which of two conflicts the editor reads off it.
  it('keeps the sentence of a code that carries its reason', async () => {
    const api = await koreanApi()
    const output = 'pvs failed: Device /dev/sdb excluded by a filter.'
    expect(refusal(api, 500, 'LVM_ERROR', output).message).toBe(output)
    expect(refusal(api, 409, 'STALE_WRITE', 'This file no longer exists').message).toBe('This file no longer exists')
  })
})

// CODE_MESSAGES has three readers: a refusal (api.ts codeText), a warning
// (warnings.ts) and why an install row ended (installRun.ts). A message that
// quotes the server's sentence must get it from each of them, or the operator
// reads the placeholder itself.
describe('{{serverMessage}}, for every reader of the code table', () => {
  const korean = async () => {
    const i18n = i18next.createInstance()
    await i18n.init({ lng: 'ko', resources: { ko: { translation: ko } }, interpolation: { escapeValue: false } })
    return i18n.t
  }
  const quoting = Object.entries(CODE_MESSAGES).filter(([, key]) => placeholders(ko, key).includes('serverMessage')).map(([code]) => code)

  it('is quoted by at least one code, so this test tests something', () => {
    expect(quoting).toContain('TOOL_NOT_INSTALLED')
  })

  it('is filled when the code arrives as a warning', async () => {
    const t = await korean()
    for (const code of quoting) {
      const text = warningText({ code, message: 'smartctl is not installed. Install smartmontools.' }, t)
      expect(text, code).not.toContain('{{')
      expect(text, code).toContain('smartctl is not installed. Install smartmontools.')
    }
  })

  it('is filled when the code ends an install row', async () => {
    const t = await korean()
    for (const code of quoting) {
      expect(installReasonText({ reason: code } as Parameters<typeof installReasonText>[0], t), code).not.toContain('{{')
    }
  })
})
