import { describe, expect, it } from 'vitest'
import { attachedText, carriedFiles, type Carried } from './terminalAttach'

const file = (name: string) => ({ name }) as File
const carried = (files: File[], text = ''): Carried => ({ files, getData: (type) => (type === 'text/plain' ? text : '<b>html</b>') })

describe('carriedFiles', () => {
  it('takes the files of a paste or a drop that is only files', () => {
    const shot = file('image.png')
    expect(carriedFiles(carried([shot]))).toEqual([shot])
    expect(carriedFiles(carried([file('a.log'), file('b.log')]))).toHaveLength(2)
  })

  it('leaves a paste that has text to be a paste of the text', () => {
    // Cells copied from a spreadsheet: the text, and a picture of it.
    expect(carriedFiles(carried([file('image.png')], 'a\tb\n1\t2'))).toEqual([])
  })

  it('takes nothing where there is nothing', () => {
    expect(carriedFiles(carried([], 'just text'))).toEqual([])
    expect(carriedFiles(carried([]))).toEqual([])
    expect(carriedFiles(null)).toEqual([])
    expect(carriedFiles(undefined)).toEqual([])
  })
})

describe('attachedText', () => {
  it('is the paths, each ready for what is typed next', () => {
    expect(attachedText(['/home/u/a.png'], '')).toBe('/home/u/a.png ')
    expect(attachedText(['/a', '/b'], 'look at ')).toBe('/a /b ')
  })

  it('does not run into the word before it', () => {
    expect(attachedText(['/a'], 'look at this')).toBe(' /a ')
    expect(attachedText(['/a'], 'line\n')).toBe('/a ')
  })

  it('is nothing for no paths', () => {
    expect(attachedText([], 'look at this')).toBe('')
  })
})
