export type IssueStatus = 'backlog' | 'todo' | 'in_progress' | 'in_review' | 'blocked' | 'done'
export type IssuePriority = 'none' | 'low' | 'medium' | 'high' | 'urgent'
export type ActorType = 'user' | 'agent'

export interface Actor {
  id: string
  type: ActorType
  name: string
  email?: string
  avatarColor: string
  initials: string
}

export interface User extends Actor {
  type: 'user'
  role: 'owner' | 'admin' | 'member'
}

export interface Agent extends Actor {
  type: 'agent'
  workspaceId: string
  role: string
  model: string
  status: 'online' | 'busy' | 'idle' | 'offline'
  description: string
  squadId: string | null
}

export interface Workspace {
  id: string
  slug: string
  name: string
  avatarColor: string
  plan: 'free' | 'pro' | 'business'
}

export interface WorkspaceMember {
  userId: string
  workspaceId: string
  role: 'owner' | 'admin' | 'member'
  status: 'active' | 'invited'
  joinedAt: string
}

export interface Project {
  id: string
  workspaceId: string
  title: string
  description: string
  icon: string
  color: string
  status: 'planned' | 'in_progress' | 'completed' | 'paused'
  leadId: string
  targetDate: string | null
  createdAt: string
}

export interface Squad {
  id: string
  workspaceId: string
  name: string
  description: string
  color: string
  memberIds: string[]
  projectIds: string[]
  createdAt: string
}

export interface Issue {
  id: string
  workspaceId: string
  identifier: string
  title: string
  description: string
  status: IssueStatus
  priority: IssuePriority
  assigneeId: string | null
  projectId: string | null
  labels: string[]
  createdAt: string
  updatedAt: string
  commentCount: number
  /**
   * Manual position within its status column on the board. Drag-and-drop
   * writes a fractional value between its new neighbors so reordering never
   * needs to renumber the rest of the column; sorting by this (not
   * updatedAt) is what makes a dropped card land exactly where the user put
   * it instead of jumping to wherever a recency sort would place it.
   */
  order: number
}

export interface ChatMessage {
  id: string
  sessionId: string
  authorId: string
  authorType: ActorType
  content: string
  createdAt: string
}

export interface ChatSession {
  id: string
  workspaceId: string
  title: string
  agentId: string
  updatedAt: string
  unreadCount: number
}

export type InboxItemType = 'mention' | 'assignment' | 'comment' | 'invite'

export interface InboxItem {
  id: string
  workspaceId: string
  type: InboxItemType
  title: string
  body: string
  actorId: string
  issueId: string | null
  read: boolean
  createdAt: string
}

export interface Runtime {
  id: string
  workspaceId: string
  name: string
  type: 'sandbox' | 'container' | 'vm'
  status: 'running' | 'stopped' | 'provisioning'
  region: string
  cpu: number
  memoryGb: number
  createdAt: string
}

export interface Invoice {
  id: string
  workspaceId: string
  date: string
  amount: number
  status: 'paid' | 'pending'
}
