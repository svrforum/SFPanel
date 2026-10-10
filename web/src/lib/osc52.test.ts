import { describe, expect, it } from 'vitest'
import { decodeOsc52 } from './osc52'

const b64 = (s: string) => btoa(String.fromCharCode(...new TextEncoder().encode(s)))

describe('decodeOsc52', () => {
  it('decodes what tmux sends for a copy, UTF-8 included', () => {
    expect(decodeOsc52(`c;${b64('docker compose logs -f 한글')}`)).toBe('docker compose logs -f 한글')
  })

  it('takes any selection field, empty included', () => {
    expect(decodeOsc52(`;${b64('a')}`)).toBe('a')
    expect(decodeOsc52(`p;${b64('b')}`)).toBe('b')
  })

  it('keeps line breaks', () => {
    expect(decodeOsc52(`c;${b64('one\ntwo')}`)).toBe('one\ntwo')
  })

  // "?" is a request to READ the clipboard back to the program.
  it('never answers a read request', () => {
    expect(decodeOsc52('c;?')).toBeNull()
  })

  it('rejects what is not a write', () => {
    expect(decodeOsc52('')).toBeNull()
    expect(decodeOsc52('c;')).toBeNull()
    expect(decodeOsc52(b64('no selection field'))).toBeNull()
    expect(decodeOsc52('c;not base64!')).toBeNull()
    // Valid base64 of bytes that are not UTF-8.
    expect(decodeOsc52(`c;${btoa(String.fromCharCode(0xff, 0xfe, 0x80))}`)).toBeNull()
  })
})
