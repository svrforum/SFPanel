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
