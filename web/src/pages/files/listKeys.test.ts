import { describe, expect, it } from 'vitest'
import { listKey, type KeyNode } from './listKeys'

function node(tagName: string, attrs: Record<string, string> = {}, parent: KeyNode | null = null, editable = false): KeyNode {
  return {
    tagName,
    isContentEditable: editable,
    parentElement: parent,
    hasAttribute: (name) => name in attrs,
    getAttribute: (name) => attrs[name] ?? null,
  }
}

// The page's own element, a row of the listing inside it, and the controls of
// that row and of the toolbar.
const page = node('DIV')
const row = node('TR', { 'data-row-index': '3' }, node('TBODY', {}, page))
const name = node('BUTTON', { 'data-row-open': '' }, node('TD', {}, row))
const checkbox = node('BUTTON', { role: 'checkbox' }, node('TD', {}, row))
const deleteButton = node('BUTTON', {}, node('TD', {}, row))
const newFolder = node('BUTTON', {}, page)
const search = node('INPUT', {}, page)
// A dialog the page renders is portalled into <body>, out of the page's
// element; React still bubbles its keys to the page's handler.
const dialogButton = node('BUTTON', {}, node('DIV', { role: 'dialog' }, node('BODY')))
const dialogCheckbox = node('BUTTON', { role: 'checkbox' }, node('DIV', { role: 'dialog' }, node('BODY')))

const press = (key: string, target: KeyNode, mods: Partial<Record<'ctrlKey' | 'altKey' | 'metaKey', boolean>> = {}) =>
  listKey({ key, ctrlKey: false, altKey: false, metaKey: false, ...mods, target, currentTarget: page })

describe('listKey', () => {
  it('leaves Enter to the button that has the focus', () => {
    // Enter on New folder opened the active row instead of the prompt.
    expect(press('Enter', newFolder)).toBeNull()
    expect(press('Enter', name)).toBeNull()
    expect(press('Enter', page)).toBeNull()
  })

  it('takes nothing that bubbles out of a portalled dialog', () => {
    // Space ticked a row behind the permissions dialog, Backspace left the
    // folder from inside a dialog, and the arrows moved the list under a menu.
    expect(press(' ', dialogCheckbox)).toBeNull()
    expect(press('Backspace', dialogButton)).toBeNull()
    expect(press('ArrowDown', dialogButton)).toBeNull()
    expect(press('Enter', dialogButton)).toBeNull()
  })

  it('selects with Space on a row\'s name, and nowhere else', () => {
    expect(press(' ', name)).toEqual({ action: 'toggle', row: 3 })
    expect(press(' ', row)).toEqual({ action: 'toggle', row: 3 })
    // The checkbox ticks itself, and a row's other buttons press as buttons.
    expect(press(' ', checkbox)).toBeNull()
    expect(press(' ', deleteButton)).toBeNull()
    expect(press(' ', newFolder)).toBeNull()
  })

  it('goes up a folder with Backspace from a row or the page itself', () => {
    expect(press('Backspace', checkbox)).toEqual({ action: 'parent' })
    expect(press('Backspace', page)).toEqual({ action: 'parent' })
    expect(press('Backspace', newFolder)).toBeNull()
  })

  it('moves between rows from anywhere on the page but a field', () => {
    expect(press('ArrowDown', newFolder)).toEqual({ action: 'move', to: 'next', row: -1 })
    expect(press('ArrowUp', name)).toEqual({ action: 'move', to: 'prev', row: 3 })
    expect(press('Home', checkbox)).toEqual({ action: 'move', to: 'first', row: 3 })
    expect(press('End', page)).toEqual({ action: 'move', to: 'last', row: -1 })
    expect(press('ArrowDown', search)).toBeNull()
    expect(press('Backspace', search)).toBeNull()
    expect(press(' ', node('DIV', {}, page, true))).toBeNull()
  })

  it('leaves a key with Ctrl, Alt or the command key alone', () => {
    expect(press('ArrowDown', name, { altKey: true })).toBeNull()
    expect(press('Backspace', page, { ctrlKey: true })).toBeNull()
    expect(press(' ', name, { metaKey: true })).toBeNull()
  })
})
