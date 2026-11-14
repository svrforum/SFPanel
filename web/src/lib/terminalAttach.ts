// What a paste or a drop brings that is meant as a file.

/** The part of a DataTransfer this needs; a ClipboardEvent's and a DragEvent's both have it. */
export interface Carried {
  files: ArrayLike<File>
  getData(type: string): string
}

/**
 * The files to attach: those a paste or a drop carries with no text beside
 * them — a screenshot on the clipboard, files dragged from a folder. Where
 * there is text too, the text is what was copied: cells copied from a
 * spreadsheet come with a picture of themselves, and pasting them into a
 * terminal must stay a paste of the cells.
 */
export function carriedFiles(data: Carried | null | undefined): File[] {
  if (!data || data.files.length === 0) return []
  if (data.getData('text/plain') !== '') return []
  return Array.from(data.files)
}

/**
 * What goes into a prompt or onto a command line for uploaded files: their
 * paths, each followed by a space, after one more where what is already
 * written does not end in one.
 */
export function attachedText(paths: string[], before: string): string {
  if (paths.length === 0) return ''
  return (/\S$/.test(before) ? ' ' : '') + paths.map((p) => `${p} `).join('')
}
