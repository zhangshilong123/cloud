/**
 * Transport-only folder → ZIP serialization. This is a pure byte-path boundary
 * adapter: it packages the browser's picked directory (a `FileList` from a
 * `<input webkitdirectory>`) into one STORE-only ZIP whose entry names are the
 * folder-relative paths — the first segment of each `webkitRelativePath` is the
 * selected directory itself and is dropped, mirroring the backend's
 * `filepath.Rel(root, p)` so a folder imports exactly like an equivalent ZIP.
 *
 * This module performs NO Skill discovery, nested-root validation,
 * `canonical_name` derivation, or content-digest computation — all of that
 * remains the backend's authority. It only detects an empty pick and rejects an
 * over-budget tree before reading any bytes, and freezes DOS timestamps so the
 * archive leaks no filesystem modification times.
 */

/** Shared with the backend's `skillsUploadBudget` (256 MiB) — enforced before reading. */
export const FOLDER_IMPORT_BUDGET_BYTES = 256 * 1024 * 1024

/** The subset of `File` this adapter needs, so tests can pass plain objects. */
export interface FolderSourceFile {
  /** `webkitRelativePath`, e.g. `<selected-dir>/sub/SKILL.md`. */
  webkitRelativePath: string
  size: number
  arrayBuffer(): Promise<ArrayBuffer>
}

/** Discriminated outcome of packaging a folder: `ok` on success, otherwise a reason to surface. */
export type FolderZipResult =
  | { kind: 'ok'; file: File; entryCount: number }
  | { kind: 'empty' }
  | { kind: 'oversize'; budgetBytes: number }

/** DOS date for 1980-01-01; time 0 — a fixed epoch so no file mtime is leaked. */
const DOS_DATE = 0x21
const UTF8_FLAG = 0x0800 // general-purpose bit 11: filename is UTF-8

const encoder = new TextEncoder()

interface Entry {
  path: string
  data: Uint8Array
  localOffset: number
}

/** Mutable cursor over the output buffer; keeps the serializers index-free. */
interface Writer {
  out: Uint8Array
  view: DataView
  pos: number
}

/**
 * Drops the selected directory's name (the first `webkitRelativePath` segment),
 * returning the entry path the backend would compute with `filepath.Rel`.
 */
export function entryPathFor(webkitRelativePath: string): string {
  const segments = webkitRelativePath.replace(/^\/+/, '').split('/')
  return segments.slice(1).join('/')
}

/** Deterministic ordering; entry paths are unique, so sorting is total. */
function byPath(a: string, b: string): number {
  if (a < b) return -1
  if (a > b) return 1
  return 0
}

// --- CRC-32 (IEEE 802.3) -----------------------------------------------------

const CRC_TABLE = new Uint32Array(256)
for (let n = 0; n < 256; n += 1) {
  let c = n
  for (let k = 0; k < 8; k += 1) {
    c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  }
  CRC_TABLE[n] = c >>> 0
}

function crc32(bytes: Uint8Array): number {
  let c = 0xffffffff
  for (const byte of bytes) {
    // `& 0xff` keeps the index in [0, 255], so the `?? 0` never fires.
    c = (CRC_TABLE[(c ^ byte) & 0xff] ?? 0) ^ (c >>> 8)
  }
  return (c ^ 0xffffffff) >>> 0
}

// --- little-endian serialization --------------------------------------------

function utf8(s: string): Uint8Array {
  return encoder.encode(s)
}

function putU16(w: Writer, value: number): void {
  w.view.setUint16(w.pos, value, true)
  w.pos += 2
}

function putU32(w: Writer, value: number): void {
  w.view.setUint32(w.pos, value, true)
  w.pos += 4
}

function putBytes(w: Writer, bytes: Uint8Array): void {
  w.out.set(bytes, w.pos)
  w.pos += bytes.length
}

/**
 * Writes the version/flags/times/crc/sizes/path-length fields that are byte-for-byte
 * identical between a local file header and its central-directory entry (both store-mode
 * records with the same frozen timestamp and the same crc + sizes).
 */
function writeSharedEntryFields(w: Writer, entry: Entry, pathBytes: Uint8Array): void {
  putU16(w, 20) // version needed
  putU16(w, UTF8_FLAG)
  putU16(w, 0) // compression method: store
  putU16(w, 0) // last mod time
  putU16(w, DOS_DATE) // last mod date
  putU32(w, crc32(entry.data))
  putU32(w, entry.data.length) // compressed size (== uncompressed for store)
  putU32(w, entry.data.length) // uncompressed size
  putU16(w, pathBytes.length)
  putU16(w, 0) // extra field length
}

function writeLocalFileHeader(w: Writer, entry: Entry): void {
  const pathBytes = utf8(entry.path)
  putU32(w, 0x04034b50)
  writeSharedEntryFields(w, entry, pathBytes)
  putBytes(w, pathBytes)
  putBytes(w, entry.data)
}

function writeCentralDirectoryEntry(w: Writer, entry: Entry): void {
  const pathBytes = utf8(entry.path)
  putU32(w, 0x02014b50)
  putU16(w, 0x0314) // version made by (Unix, spec 2.0)
  writeSharedEntryFields(w, entry, pathBytes)
  putU16(w, 0) // file comment length
  putU16(w, 0) // disk number start
  putU16(w, 0) // internal file attributes
  putU32(w, 0) // external file attributes
  putU32(w, entry.localOffset) // relative offset of local header
  putBytes(w, pathBytes)
}

function writeEndOfCentralDirectory(
  w: Writer,
  count: number,
  centralSize: number,
  centralStart: number,
): void {
  putU32(w, 0x06054b50)
  putU16(w, 0) // number of this disk
  putU16(w, 0) // disk where central directory starts
  putU16(w, count) // entries on this disk
  putU16(w, count) // total entries
  putU32(w, centralSize)
  putU32(w, centralStart)
  putU16(w, 0) // comment length
}

/**
 * Packages the picked directory into one ZIP archive and returns it as a `File`
 * for the caller to upload via the existing `POST /skills/imports` with
 * `source_kind: 'zip'`.
 *
 * @returns a discriminated result: the built archive, an empty folder, or an
 *   over-budget tree; callers branch on `kind` and never inspect raw bytes.
 */
export async function folderToZip(files: readonly FolderSourceFile[]): Promise<FolderZipResult> {
  // Only a genuinely empty pick is "empty" (per spec §5): every picked file is packaged,
  // and the first path segment is stripped as the selected root — never used to drop files.
  if (files.length === 0) {
    return { kind: 'empty' }
  }
  const totalBytes = files.reduce((sum, file) => sum + file.size, 0)
  if (totalBytes > FOLDER_IMPORT_BUDGET_BYTES) {
    return { kind: 'oversize', budgetBytes: FOLDER_IMPORT_BUDGET_BYTES }
  }
  const rootName = (files[0]?.webkitRelativePath.split('/')[0] ?? 'folder') || 'folder'

  const named = files
    .map((file) => ({ file, path: entryPathFor(file.webkitRelativePath) }))
    .toSorted((a, b) => byPath(a.path, b.path))

  const raw = await Promise.all(
    named.map((item) =>
      item.file.arrayBuffer().then((buffer) => ({ path: item.path, data: new Uint8Array(buffer) })),
    ),
  )

  const entries: Entry[] = []
  let centralStart = 0
  for (const { path, data } of raw) {
    entries.push({ path, data, localOffset: centralStart })
    centralStart += 30 + utf8(path).length + data.length
  }
  let centralSize = 0
  for (const entry of entries) {
    centralSize += 46 + utf8(entry.path).length
  }

  const out = new Uint8Array(centralStart + centralSize + 22)
  const writer: Writer = { out, view: new DataView(out.buffer), pos: 0 }
  for (const entry of entries) {
    writeLocalFileHeader(writer, entry)
  }
  for (const entry of entries) {
    writeCentralDirectoryEntry(writer, entry)
  }
  writeEndOfCentralDirectory(writer, entries.length, centralSize, centralStart)

  const file = new File([out], `${rootName}.zip`, { type: 'application/zip' })
  return { kind: 'ok', file, entryCount: entries.length }
}
