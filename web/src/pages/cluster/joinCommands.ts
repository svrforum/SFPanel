// The installer the README's one-liner fetches; the token page prints the same line.
export const INSTALL_SCRIPT_URL = 'https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh'

// The join command, and the installer chained to it for a server without
// SFPanel, as `sfpanel cluster token` prints them. --ca pins the cluster CA so
// the new node verifies the leader before sending the token. asRoot leaves
// sudo out:
// Proxmox VE has no sudo, root is its administrator, and neither has Debian
// installed with a root password. There a command that starts with sudo
// stops the && chain before anything is installed.
export function joinCommands(leader: string, grpcPort: number, token: string, caPin: string, asRoot: boolean) {
  const sudo = asRoot ? '' : 'sudo '
  const join = `${sudo}sfpanel cluster join ${leader}:${grpcPort} ${token} --ca ${caPin}`
  return { join, installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | ${sudo}bash && ${join}` }
}

// What a node may be called: internal/cluster/agent_join.go validNodeName.
// The agent refuses any other name, so a command carrying one cannot work.
export const NODE_NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$/

// The agent's join lines, as `sfpanel cluster token --agent` prints them
// (agentJoinInstructions): a server running SFPanel joins with `sfpanel agent
// join`, and a new one hands the same arguments to the installer, which
// installs and joins in one step. A name goes last on both; without one the
// agent takes its host name.
export function agentJoinCommands(manager: string, grpcPort: number, token: string, caPin: string, asRoot: boolean, name?: string) {
  const sudo = asRoot ? '' : 'sudo '
  const args = `${manager}:${grpcPort} ${token} --ca ${caPin}${name ? ` --name ${name}` : ''}`
  return {
    join: `${sudo}sfpanel agent join ${args}`,
    installAndJoin: `curl -fsSL ${INSTALL_SCRIPT_URL} | ${sudo}bash -s -- --agent ${args}`,
  }
}
