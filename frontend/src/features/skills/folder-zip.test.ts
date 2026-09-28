import { describe, expect, it } from 'vitest'
import { entryPathFor, FOLDER_IMPORT_BUDGET_BYTES, folderToZip } from './folder-zip'
import type { FolderSourceFile, FolderZipResult } from './folder-zip'

function file(relativePath: string, content: string): FolderSourceFile {
  const bytes = new TextEncoder().encode(content)
  return {
    webkitRelativePath: relativePath,
    size: bytes.length,
    arrayBuffer: () => Promise.resolve(bytes.slice().buffer),
  }
}

async function okResult(result: FolderZipResult): Promise<File> {
  if (result.kind !== 'ok') throw new Error(`expected ok, got ${result.kind}`)
  return result.file
}

/** Reads the central directory entry names out of a ZIP byte buffer. */
function readZipEntryNames(buffer: ArrayBuffer): string[] {
  const view = new DataView(buffer)
  const bytes = new Uint8Array(buffer)
  const eocd = buffer.byteLength - 22
  expect(view.getUint32(eocd, true)).toBe(0x06054b50)
  const count = view.getUint16(eocd + 10, true)
  const centralOffset = view.getUint32(eocd + 16, true)

  const names: string[] = []
  let pos = centralOffset
  for (let i = 0; i < count; i += 1) {
    expect(view.getUint32(pos, true)).toBe(0x02014b50)
    const nameLen = view.getUint16(pos + 28, true)
    const extraLen = view.getUint16(pos + 30, true)
    const commentLen = view.getUint16(pos + 32, true)
    names.push(new TextDecoder().decode(bytes.slice(pos + 46, pos + 46 + nameLen)))
    pos += 46 + nameLen + extraLen + commentLen
  }
  return names
}

describe('entryPathFor', () => {
  it('strips the selected directory and preserves relative nesting', () => {
    expect(entryPathFor('MySkill/SKILL.md')).toBe('SKILL.md')
    expect(entryPathFor('MySkill/sub/dir/file.txt')).toBe('sub/dir/file.txt')
  })
})

describe('folderToZip', () => {
  it('packages a folder into a ZIP with root-stripped, sorted relative paths', async () => {
    const result = await folderToZip([file('MySkill/sub/b.md', 'b'), file('MySkill/a.md', 'a')])
    const archive = await okResult(result)
    expect(archive.name).toBe('MySkill.zip')

    const names = readZipEntryNames(await archive.arrayBuffer())
    expect(names).toEqual(['a.md', 'sub/b.md'])
  })

  it('preserves file bytes verbatim inside the STORE archive', async () => {
    const archive = await okResult(await folderToZip([file('MySkill/SKILL.md', 'hello world')]))
    const bytes = new Uint8Array(await archive.arrayBuffer())
    const text = new TextDecoder().decode(bytes)
    expect(text).toContain('hello world')
  })

  it('rejects an empty pick', async () => {
    await expect(folderToZip([])).resolves.toEqual({ kind: 'empty' })
  })

  it('never reports a non-empty pick as empty, even for a single root-level file', async () => {
    const result = await folderToZip([file('MySkill', 'x')])
    expect(result.kind).toBe('ok')
  })

  it('rejects an over-budget tree before reading bytes', async () => {
    const big = file('MySkill/SKILL.md', 'x')
    big.size = FOLDER_IMPORT_BUDGET_BYTES + 1
    await expect(folderToZip([big])).resolves.toEqual({
      kind: 'oversize',
      budgetBytes: FOLDER_IMPORT_BUDGET_BYTES,
    })
  })
})
