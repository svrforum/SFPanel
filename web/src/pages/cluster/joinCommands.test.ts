import { describe, expect, it } from 'vitest'
import { INSTALL_SCRIPT_URL, joinCommands } from './joinCommands'

describe('joinCommands', () => {
  it('keeps sudo by default', () => {
    expect(joinCommands('192.0.2.10', 3629, 'tok123', false)).toEqual({
      join: 'sudo sfpanel cluster join 192.0.2.10:3629 tok123',
      installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | sudo bash && sudo sfpanel cluster join 192.0.2.10:3629 tok123`,
    })
  })

  // A server without sudo (Proxmox VE) stops the && chain at the first sudo.
  it('as root, has no sudo anywhere', () => {
    expect(joinCommands('192.0.2.10', 3629, 'tok123', true)).toEqual({
      join: 'sfpanel cluster join 192.0.2.10:3629 tok123',
      installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | bash && sfpanel cluster join 192.0.2.10:3629 tok123`,
    })
  })
})
