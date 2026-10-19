import { describe, expect, it } from 'vitest'
import { INSTALL_SCRIPT_URL, joinCommands } from './joinCommands'

const pin = 'sha256:' + 'ab'.repeat(32)

describe('joinCommands', () => {
  it('keeps sudo by default, and pins the cluster CA', () => {
    expect(joinCommands('192.0.2.10', 3629, 'tok123', pin, false)).toEqual({
      join: `sudo sfpanel cluster join 192.0.2.10:3629 tok123 --ca ${pin}`,
      installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | sudo bash && sudo sfpanel cluster join 192.0.2.10:3629 tok123 --ca ${pin}`,
    })
  })

  // A server without sudo (Proxmox VE) stops the && chain at the first sudo.
  it('as root, has no sudo anywhere', () => {
    expect(joinCommands('192.0.2.10', 3629, 'tok123', pin, true)).toEqual({
      join: `sfpanel cluster join 192.0.2.10:3629 tok123 --ca ${pin}`,
      installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | bash && sfpanel cluster join 192.0.2.10:3629 tok123 --ca ${pin}`,
    })
  })
})
