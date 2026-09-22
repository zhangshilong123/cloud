import { useSyncExternalStore } from 'react'
import type { User } from '@/mocks/data/types'

/**
 * Demo-plane session for the MSW-mocked store (`/mock-api/*`), kept separate
 * from the cloud session (verified through `/api/v1/me`). It is purely local
 * mock state persisted to localStorage and never reaches the real backend.
 *
 * Hand-rolled with `useSyncExternalStore` (the zustand dependency the merged
 * tree dropped) so the demo plane stays dependency-free.
 */
export interface DemoSession {
  token: string | null
  user: User | null
}

const STORAGE_KEY = 'ora-mock-auth'

/** Reads the persisted session; corrupted or unavailable storage signs out. */
function readStored(): DemoSession {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { token: null, user: null }
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return { token: null, user: null }
    const candidate = parsed as { token?: unknown; user?: unknown }
    if (typeof candidate.token === 'string' && isUser(candidate.user)) {
      return { token: candidate.token, user: candidate.user }
    }
  } catch {
    // corrupted or unavailable storage: the session simply does not persist
  }
  return { token: null, user: null }
}

function isUser(value: unknown): value is User {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as { id?: unknown }).id === 'string'
  )
}

let session: DemoSession = readStored()
const listeners = new Set<() => void>()

function emit(): void {
  for (const listener of listeners) listener()
}

function setSession(token: string, user: User): void {
  session = { token, user }
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(session))
  } catch {
    // storage unavailable: the session still lives for this tab
  }
  emit()
}

function clear(): void {
  session = { token: null, user: null }
  try {
    localStorage.removeItem(STORAGE_KEY)
  } catch {
    // ignore
  }
  emit()
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/**
 * Selector-based demo session hook, mirroring the zustand API it replaces:
 * `useDemoAuthStore((s) => s.token)`. Consumers re-render when the selected
 * slice changes.
 */
export function useDemoAuthStore<T>(selector: (s: DemoSession) => T): T {
  return useSyncExternalStore(subscribe, () => selector(session))
}

/** Imperative demo-session actions for event handlers outside React. */
export const demoAuthStore = { setSession, clear }
