import { test, expect } from '@playwright/test'

// Verifies that a password change applied on the leader replicates to the
// follower via the Raft FSM (CmdSetAccount). After the change, login on
// the follower must accept the NEW password and reject the OLD one.
//
// Required env vars:
//   PW_BASE_URL       Leader URL            (required)
//   PW_FOLLOWER_URL   Follower URL          (required — direct, not proxied)
//   PW_USER, PW_PASS  Current admin creds   (required)

const leaderURL = process.env.PW_BASE_URL
const followerURL = process.env.PW_FOLLOWER_URL
const user = process.env.PW_USER
const pass = process.env.PW_PASS

const haveEnv = leaderURL && followerURL && user && pass
const skipReason = 'requires PW_BASE_URL, PW_FOLLOWER_URL, PW_USER, PW_PASS + live 2-node cluster'

async function loginAgainst(request: import('@playwright/test').APIRequestContext, url: string, username: string, password: string) {
  return request.post(`${url}/api/v1/auth/login`, {
    headers: { 'Content-Type': 'application/json' },
    data: { username, password },
  })
}

test.describe('Cluster FSM replicates admin password', () => {
  test.skip(!haveEnv, skipReason)

  test('new password works on follower, old password is rejected', async ({ request }) => {
    test.setTimeout(30000)
    const oldPass = pass!
    const newPass = `rotated-${Date.now()}-test!`

    // 1) Sanity: both nodes accept the current password.
    const leaderBefore = await (await loginAgainst(request, leaderURL!, user!, oldPass)).json()
    expect(leaderBefore.success).toBe(true)
    const tokenL = leaderBefore.data.token as string

    const followerBefore = await (await loginAgainst(request, followerURL!, user!, oldPass)).json()
    expect(followerBefore.success).toBe(true)

    // 2) Rotate the password on the leader via the normal API.
    const change = await request.post(`${leaderURL}/api/v1/auth/change-password`, {
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${tokenL}` },
      data: { current_password: oldPass, new_password: newPass },
    })
    const changeJson = await change.json()
    expect(changeJson.success).toBe(true)

    try {
      // 3) Poll the follower — Raft Apply + SQLite write is fast but
      // not synchronous from the caller's perspective.
      let followerAccepted = false
      for (let i = 0; i < 20; i++) {
        const res = await loginAgainst(request, followerURL!, user!, newPass)
        const j = await res.json()
        if (j.success) { followerAccepted = true; break }
        await new Promise((r) => setTimeout(r, 500))
      }
      expect(followerAccepted, 'follower did not accept the new password within 10s').toBe(true)

      // 4) Old password must now be rejected on the follower.
      const stale = await loginAgainst(request, followerURL!, user!, oldPass)
      const staleJson = await stale.json()
      expect(staleJson.success).toBe(false)
    } finally {
      // 5) Always revert — even if an assertion above failed — so the
      // running panel stays on the operator's original credentials.
      const loginNew = await (await loginAgainst(request, leaderURL!, user!, newPass)).json()
      if (loginNew.success) {
        await request.post(`${leaderURL}/api/v1/auth/change-password`, {
          headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${loginNew.data.token}` },
          data: { current_password: newPass, new_password: oldPass },
        })
      }
    }
  })
})
