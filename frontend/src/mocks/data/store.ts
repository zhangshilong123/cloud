import {
  agents,
  chatMessages,
  chatSessions,
  currentUserId,
  inboxItems,
  invoices,
  issues,
  members,
  projects,
  runtimes,
  squads,
  users,
  workspaces,
} from './seed'
import type { Workspace } from './types'
/**
 * Mutable in-memory copies of the seed data. MSW handlers read and write
 * through this module so create/update/delete calls persist for the life
 * of the tab without a real backend.
 */
const seededWorkspaces: Workspace[] = workspaces.map((w) => Object.assign({}, w))

type NonEmpty<T> = [T, ...T[]]

function asNonEmpty<T>(items: T[], name: string): NonEmpty<T> {
  const [first, ...rest] = items
  if (!first) {
    throw new Error(`${name} seed data must not be empty`)
  }
  return [first, ...rest]
}

const defaultWorkspace = seededWorkspaces[0]
if (!defaultWorkspace) {
  throw new Error('workspace seed data must not be empty')
}

export const db = {
  /** The default workspace, used wherever a workspace isn't resolved from a route param. */
  workspace: defaultWorkspace,
  workspaces: seededWorkspaces,
  users: asNonEmpty([...users], 'users'),
  members: [...members],
  agents: asNonEmpty([...agents], 'agents'),
  squads: asNonEmpty([...squads], 'squads'),
  projects: asNonEmpty([...projects], 'projects'),
  issues: asNonEmpty([...issues], 'issues'),
  chatSessions: asNonEmpty([...chatSessions], 'chat sessions'),
  chatMessages: [...chatMessages],
  inboxItems: asNonEmpty([...inboxItems], 'inbox items'),
  runtimes: asNonEmpty([...runtimes], 'runtimes'),
  invoices: asNonEmpty([...invoices], 'invoices'),
}

export { currentUserId }

export function actorById(id: string | null | undefined) {
  if (!id) return undefined
  return [...db.users, ...db.agents].find((a) => a.id === id)
}

let nextIssueSeq = db.issues.length + 1
export function nextIssueIdentifier(): string {
  return `ORA-${100 + nextIssueSeq++}`
}

export function workspaceBySlug(slug: string | undefined): Workspace | undefined {
  // The route slug is a mock workspace slug in demo mode; callers fall back to
  // the seeded workspace where a preview needs one.
  return db.workspaces.find((w) => w.slug === slug)
}

let idCounter = 1
export function nextId(prefix: string): string {
  return `${prefix}-${Date.now()}-${idCounter++}`
}
