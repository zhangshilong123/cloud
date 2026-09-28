/**
 * Shortens a content digest (`sha256:…`) for compact list rendering; the full
 * value stays available on the detail view for copying.
 */
export function shortDigest(digest: string): string {
  return digest.length <= 24 ? digest : `${digest.slice(0, 12)}…${digest.slice(-8)}`
}

/** Human-readable byte count for skill revision sizes. */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / 1024 ** 2).toFixed(1)} MiB`
}
