import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Tests the document-start asset. Native Keystore/origin enforcement is checked
// separately on Android; this mock only records the asset's outbound messages.
test('Android session restores before app startup and publishes complete token pairs', async ({ page }) => {
  const template = readFileSync(resolve('../android/app/src/main/assets/session.js'), 'utf8')
  const saved = { token: 'saved-access', refresh_token: 'saved-refresh' }
  await page.route('http://session.test/**', route => route.fulfill({
    contentType: 'text/html',
    body: '<script>window.boot = [sessionStorage.getItem("token"), sessionStorage.getItem("refresh_token")]</script>',
  }))
  await page.addInitScript({ content: `
    window.messages = [];
    window.sfpanelSessionState = { postMessage: value => window.messages.push(JSON.parse(value)) };
    ${template.replace('__SFPANEL_SESSION__', JSON.stringify(saved))}
  ` })
  await page.goto('http://session.test/')
  expect(await page.evaluate('window.boot')).toEqual(['saved-access', 'saved-refresh'])
  expect(await page.evaluate('window.messages')).toEqual([saved])
  await page.evaluate(() => {
    sessionStorage.setItem('token', 'rotated-access')
    sessionStorage.setItem('refresh_token', 'rotated-refresh')
    localStorage.setItem('token', 'unrelated')
    sessionStorage.setItem('theme', 'dark')
  })
  expect(await page.evaluate('window.messages')).toEqual([
    saved, { token: 'rotated-access', refresh_token: 'rotated-refresh' },
  ])
  await page.evaluate(() => {
    sessionStorage.removeItem('token')
    sessionStorage.removeItem('refresh_token')
  })
  expect(await page.evaluate('window.messages.at(-1)')).toEqual({ token: null, refresh_token: null })
  await page.evaluate(() => {
    sessionStorage.setItem('token', 'new-login')
    sessionStorage.setItem('refresh_token', 'new-refresh')
  })
  await page.evaluate(() => sessionStorage.clear())
  expect(await page.evaluate('window.messages.at(-1)')).toEqual({ token: null, refresh_token: null })
})
