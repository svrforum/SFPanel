import { describe, expect, it } from 'vitest'
import { INSTALL_SCRIPT_URL, NODE_NAME_PATTERN, agentJoinCommands, joinCommands } from './joinCommands'

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

// The same lines `sfpanel cluster token --agent` prints (agentJoinInstructions
// in cmd/sfpanel/cluster_commands.go), with --name at the end when one is given.
describe('agentJoinCommands', () => {
  const args = `192.0.2.10:3629 tok123 --ca ${pin}`

  it('keeps sudo by default', () => {
    expect(agentJoinCommands('192.0.2.10', 3629, 'tok123', pin, false)).toEqual({
      join: `sudo sfpanel agent join ${args}`,
      installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | sudo bash -s -- --agent ${args}`,
    })
  })

  it('as root, has no sudo anywhere', () => {
    expect(agentJoinCommands('192.0.2.10', 3629, 'tok123', pin, true)).toEqual({
      join: `sfpanel agent join ${args}`,
      installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | bash -s -- --agent ${args}`,
    })
  })

  it('ends both lines with the name it is given', () => {
    expect(agentJoinCommands('192.0.2.10', 3629, 'tok123', pin, false, 'agent-07')).toEqual({
      join: `sudo sfpanel agent join ${args} --name agent-07`,
      installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | sudo bash -s -- --agent ${args} --name agent-07`,
    })
  })

  it('as root with a name', () => {
    expect(agentJoinCommands('192.0.2.10', 3629, 'tok123', pin, true, 'agent-07')).toEqual({
      join: `sfpanel agent join ${args} --name agent-07`,
      installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | bash -s -- --agent ${args} --name agent-07`,
    })
  })
})

// internal/cluster/agent_join.go validNodeName: what the agent would refuse.
describe('NODE_NAME_PATTERN', () => {
  it.each(['agent-07', 'a', 'web_1.lan', 'A' + 'x'.repeat(62)])('takes %s', (name) => {
    expect(NODE_NAME_PATTERN.test(name)).toBe(true)
  })
  it.each(['bad name!', '-lead', '.hidden', 'x'.repeat(64), '', 'a/b'])('refuses %s', (name) => {
    expect(NODE_NAME_PATTERN.test(name)).toBe(false)
  })
})
