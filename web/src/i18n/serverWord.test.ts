import { expect, it } from 'vitest'
import ko from './locales/ko.json'
import en from './locales/en.json'

const leaves = (o: unknown, p = ''): [string, string][] =>
  typeof o === 'string'
    ? [[p, o]]
    : Object.entries(o as Record<string, unknown>).flatMap(([k, v]) => leaves(v, p ? `${p}.${k}` : k))
const at = (tree: unknown, key: string): unknown =>
  key.split('.').reduce<unknown>((n, p) => (n && typeof n === 'object' ? (n as Record<string, unknown>)[p] : undefined), tree)

it("Korean calls a machine a server, except where it quotes Proxmox's own screens", () => {
  const left = leaves(ko).filter(([, v]) => v.replace(/Proxmox 노드|노드 →/g, '').includes('노드')).map(([k]) => k)
  expect(left).toEqual([])
})

it("Proxmox's own menu paths are quoted as Proxmox says them", () => {
  expect(at(ko, 'network.readOnly')).toContain('노드 → 시스템 → 네트워크')
})

it('the role names stay', () => {
  expect(at(ko, 'cluster.kind.manager')).toBe('매니저')
  expect(at(ko, 'cluster.kind.agent')).toBe('에이전트')
})

it('English keeps node', () => {
  expect(at(en, 'cluster.nodes.open')).toBe('Open node')
  expect(at(en, 'cluster.tasks.run.node')).toBe('Node')
})

it('a sentence about the cluster registration and the machine is not "the server of the server"', () => {
  expect(leaves(ko).filter(([, v]) => /서버의 서버|서버 서버/.test(v)).map(([k]) => k)).toEqual([])
})
