import { faker, fakerZH_CN } from '@faker-js/faker'
import type {
  Agent,
  ChatMessage,
  ChatSession,
  InboxItem,
  Invoice,
  Issue,
  IssuePriority,
  IssueStatus,
  Project,
  Runtime,
  Squad,
  User,
  Workspace,
  WorkspaceMember,
} from './types'

faker.seed(42)
fakerZH_CN.seed(42)

const AVATAR_COLORS = [
  '#f97316',
  '#f43f5e',
  '#8b5cf6',
  '#3b82f6',
  '#10b981',
  '#eab308',
  '#06b6d4',
  '#ec4899',
]

const ISSUE_STATUSES: IssueStatus[] = [
  'backlog',
  'todo',
  'in_progress',
  'in_review',
  'blocked',
  'done',
]
const ISSUE_PRIORITIES: IssuePriority[] = ['none', 'low', 'medium', 'high', 'urgent']
const LABELS = ['缺陷', '功能', '设计', '基础设施', '文档', '性能', '安全']

function pick<T>(arr: T[]): T {
  const value = faker.helpers.arrayElement(arr)
  if (value === undefined) {
    throw new Error('seed data choices must not be empty')
  }
  return value
}

function initialsOf(name: string): string {
  const parts = name.trim().split(/\s+/)
  return parts
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase() ?? '')
    .join('')
}

function colorFor(seed: string): string {
  let hash = 0
  for (let i = 0; i < seed.length; i++) hash = (hash * 31 + seed.charCodeAt(i)) >>> 0
  const color = AVATAR_COLORS[hash % AVATAR_COLORS.length]
  if (!color) {
    throw new Error('avatar color seed data must not be empty')
  }
  return color
}

// --- Chinese content generators -------------------------------------------
// faker's lorem/hacker modules aren't localized, so issue/description/chat
// copy is generated from small hand-written Chinese phrase banks instead.

const MODULES = [
  '登录',
  '支付',
  '通知',
  '搜索',
  '工作台',
  '看板',
  '聊天',
  '智能体调度',
  '计费',
  '权限',
  '数据同步',
  '缓存',
  'API 网关',
  '任务队列',
  '监控告警',
  '构建流水线',
  '文件上传',
  '导出功能',
  '邮件通知',
  '移动端',
]
const PROBLEM_WORDS = [
  '崩溃',
  '内存泄漏',
  '响应超时',
  '样式错乱',
  '数据不一致',
  '并发冲突',
  '性能下降',
  '空指针异常',
  '死锁',
  '重复请求',
  '偶发失败',
  '权限校验失败',
]
const ASPECTS = [
  '加载速度',
  '响应时间',
  '稳定性',
  '可读性',
  '可维护性',
  '安全性',
  '兼容性',
  '首屏渲染',
]
const FEATURES = [
  '批量操作',
  '数据导出',
  '高级筛选',
  '自定义排序',
  '多语言支持',
  '暗色模式',
  '快捷键',
  '审计日志',
  '权限分组',
  '自动重试',
]

const TITLE_TEMPLATES: (() => string)[] = [
  () => `修复${pick(MODULES)}模块中的${pick(PROBLEM_WORDS)}问题`,
  () => `优化${pick(MODULES)}的${pick(ASPECTS)}`,
  () => `为${pick(MODULES)}添加${pick(FEATURES)}功能`,
  () => `重构${pick(MODULES)}相关代码`,
  () => `调查${pick(MODULES)}偶发的${pick(PROBLEM_WORDS)}`,
  () => `更新${pick(MODULES)}相关文档`,
  () => `升级${pick(MODULES)}依赖版本`,
  () => `${pick(MODULES)}在移动端显示异常`,
  () => `排查${pick(MODULES)}的${pick(PROBLEM_WORDS)}`,
]

function randomIssueTitle(): string {
  return pick(TITLE_TEMPLATES)()
}

const DESC_SENTENCES = [
  '用户反馈该问题会导致操作中断，需要尽快定位根因。',
  '目前复现步骤尚不稳定，建议补充日志埋点。',
  '相关改动涉及多个下游服务，需要评估影响面。',
  '已在测试环境验证过一次，线上环境仍需观察。',
  '建议先在灰度环境验证，再全量发布。',
  '可以参考类似问题的历史修复方案。',
  '需要和产品确认预期行为后再继续处理。',
  '相关埋点数据显示近一周出现频率有所上升。',
  '修复后需要补充对应的自动化测试用例。',
  '此问题优先级较高，影响到核心链路。',
]

function randomDescription(): string {
  const paragraphCount = faker.number.int({ min: 1, max: 2 })
  return Array.from({ length: paragraphCount }, () => {
    const sentenceCount = faker.number.int({ min: 2, max: 4 })
    return Array.from({ length: sentenceCount }, () => pick(DESC_SENTENCES)).join('')
  }).join('\n\n')
}

const CHAT_FILLER_SENTENCES = [
  '这个问题我已经在跟进了。',
  '能帮我看一下具体是什么原因吗？',
  '好的，我这就去处理。',
  '已经复现了，正在定位。',
  '这个改动大概什么时候能上线？',
  '辛苦了，麻烦优先看一下这个。',
  '我这边测试没问题，可以合并。',
  '收到，稍后同步进展。',
  '这个是不是和上次的问题有关联？',
  '先这样处理，后续再优化。',
]

export const currentUserId = 'user-you'

export const workspace: Workspace = {
  id: 'ws-1',
  slug: 'ora-demo',
  name: 'Ora 演示工作区',
  avatarColor: colorFor('Ora 演示工作区'),
  plan: 'pro',
}

export const otherWorkspaces: Workspace[] = [
  {
    id: 'ws-2',
    slug: 'ora-labs',
    name: 'Ora 实验室',
    avatarColor: colorFor('Ora 实验室'),
    plan: 'free',
  },
  {
    id: 'ws-3',
    slug: 'personal',
    name: '个人空间',
    avatarColor: colorFor('个人空间'),
    plan: 'free',
  },
]

export const workspaces: Workspace[] = [workspace, ...otherWorkspaces]

export const users: User[] = [
  {
    id: currentUserId,
    type: 'user',
    name: 'Ruihao Zhang',
    email: 'ruihao053@gmail.com',
    avatarColor: colorFor('Ruihao Zhang'),
    initials: 'RZ',
    role: 'owner',
  },
  ...Array.from({ length: 7 }, () => {
    const name = fakerZH_CN.person.fullName()
    return {
      id: faker.string.uuid(),
      type: 'user' as const,
      name,
      // Emails stay ASCII regardless of display-name locale.
      email: faker.internet.email().toLowerCase(),
      avatarColor: colorFor(name),
      initials: initialsOf(name),
      role: faker.helpers.arrayElement(['admin', 'member', 'member', 'member'] as const),
    }
  }),
]

export const members: WorkspaceMember[] = [
  ...users.map((u) => ({
    userId: u.id,
    workspaceId: workspace.id,
    role: u.role,
    status: 'active' as const,
    joinedAt: faker.date.past({ years: 1 }).toISOString(),
  })),
  ...otherWorkspaces.map((ws) => ({
    userId: currentUserId,
    workspaceId: ws.id,
    role: 'owner' as const,
    status: 'active' as const,
    joinedAt: faker.date.past({ years: 1 }).toISOString(),
  })),
]

const AGENT_ROLES = [
  { role: '后端工程师', model: 'claude-sonnet-5' },
  { role: '前端工程师', model: 'claude-sonnet-5' },
  { role: '测试工程师', model: 'claude-haiku-4-5' },
  { role: '代码评审员', model: 'claude-opus-5' },
  { role: '支持分诊专员', model: 'claude-haiku-4-5' },
  { role: '发布经理', model: 'claude-sonnet-5' },
]

export const squads: Squad[] = [
  {
    id: 'squad-platform',
    workspaceId: workspace.id,
    name: '平台组',
    description: '负责核心服务、基础设施与部署流水线。',
    color: colorFor('平台组'),
    memberIds: [],
    projectIds: [],
    createdAt: faker.date.past({ years: 1 }).toISOString(),
  },
  {
    id: 'squad-product',
    workspaceId: workspace.id,
    name: '产品组',
    description: '端到端交付面向客户的产品功能。',
    color: colorFor('产品组'),
    memberIds: [],
    projectIds: [],
    createdAt: faker.date.past({ years: 1 }).toISOString(),
  },
]

export const agents: Agent[] = AGENT_ROLES.map(({ role, model }, i) => {
  const name = fakerZH_CN.person.fullName()
  const squad = squads[i % squads.length]
  if (!squad) {
    throw new Error('agent seed data requires a squad')
  }
  const squadId = squad.id
  const id = `agent-${i + 1}`
  squad.memberIds.push(id)
  return {
    id,
    type: 'agent' as const,
    workspaceId: workspace.id,
    name,
    avatarColor: colorFor(name),
    initials: initialsOf(name),
    role,
    model,
    status: faker.helpers.arrayElement(['online', 'busy', 'idle', 'offline']),
    description: `自动化${role}，负责工作区内的${pick(['需求分诊', '代码实现', '代码评审', '系统监控'])}工作。`,
    squadId,
  }
})

const PROJECT_DESCRIPTIONS = [
  '提升整体转化率，降低用户流失。',
  '统一多端体验，减少重复开发成本。',
  '完善核心指标监控，缩短故障定位时间。',
  '打通上下游系统，实现数据自动同步。',
  '为企业客户提供更灵活的计费与结算能力。',
]

export const projects: Project[] = [
  '新人引导流程改版',
  '计费系统 v2',
  '移动端功能对齐',
  '智能体市场',
  '可观测性体系重构',
].map((title, i) => {
  const id = `project-${i + 1}`
  const squad = squads[i % squads.length]
  if (!squad) {
    throw new Error('project seed data requires a squad')
  }
  squad.projectIds.push(id)
  return {
    id,
    workspaceId: workspace.id,
    title,
    description: pick(PROJECT_DESCRIPTIONS),
    icon: faker.helpers.arrayElement(['Rocket', 'Layers', 'Boxes', 'GitBranch', 'Gauge']),
    color: colorFor(title),
    status: faker.helpers.arrayElement([
      'planned',
      'in_progress',
      'in_progress',
      'completed',
      'paused',
    ]),
    leadId: faker.helpers.arrayElement(users).id,
    targetDate: faker.date.soon({ days: 90 }).toISOString(),
    createdAt: faker.date.past({ years: 1 }).toISOString(),
  }
})

const allActorIds = [...users.map((u) => u.id), ...agents.map((a) => a.id)]

const orderByStatus: Record<IssueStatus, number> = {
  backlog: 0,
  todo: 0,
  in_progress: 0,
  in_review: 0,
  blocked: 0,
  done: 0,
}

export const issues: Issue[] = Array.from({ length: 48 }, (_, i) => {
  const createdAt = faker.date.past({ years: 1 })
  const status = faker.helpers.arrayElement(ISSUE_STATUSES)
  return {
    id: `issue-${i + 1}`,
    workspaceId: workspace.id,
    identifier: `ORA-${100 + i}`,
    title: randomIssueTitle(),
    description: randomDescription(),
    status,
    priority: faker.helpers.arrayElement(ISSUE_PRIORITIES),
    assigneeId:
      faker.helpers.maybe(() => faker.helpers.arrayElement(allActorIds), { probability: 0.85 }) ??
      null,
    projectId:
      faker.helpers.maybe(() => faker.helpers.arrayElement(projects).id, { probability: 0.75 }) ??
      null,
    labels: faker.helpers.arrayElements(LABELS, { min: 0, max: 3 }),
    createdAt: createdAt.toISOString(),
    updatedAt: faker.date.between({ from: createdAt, to: new Date() }).toISOString(),
    commentCount: faker.number.int({ min: 0, max: 12 }),
    order: orderByStatus[status]++,
  }
})

export const chatSessions: ChatSession[] = agents.slice(0, 5).map((agent, i) => ({
  id: `chat-${i + 1}`,
  workspaceId: workspace.id,
  title: agent.name,
  agentId: agent.id,
  updatedAt: faker.date.recent({ days: 3 }).toISOString(),
  unreadCount: faker.number.int({ min: 0, max: 4 }),
}))

export const chatMessages: ChatMessage[] = chatSessions.flatMap((session) => {
  const count = faker.number.int({ min: 4, max: 14 })
  let t = faker.date.recent({ days: 3 })
  return Array.from({ length: count }, (_, i) => {
    t = new Date(t.getTime() + faker.number.int({ min: 60_000, max: 3_600_000 }))
    const isUser = i % 2 === 0
    return {
      id: `${session.id}-msg-${i + 1}`,
      sessionId: session.id,
      authorId: isUser ? currentUserId : session.agentId,
      authorType: isUser ? ('user' as const) : ('agent' as const),
      content: pick(CHAT_FILLER_SENTENCES),
      createdAt: t.toISOString(),
    }
  })
})

export const inboxItems: InboxItem[] = Array.from({ length: 20 }, (_, i) => {
  const type = faker.helpers.arrayElement(['mention', 'assignment', 'comment', 'invite'] as const)
  const issue = faker.helpers.maybe(() => faker.helpers.arrayElement(issues), { probability: 0.7 })
  let title = '工作区邀请'
  if (type === 'mention') title = `有人在 ${issue?.identifier ?? '一个讨论'} 中提到了你`
  else if (type === 'assignment') title = `任务已分配给你：${issue?.identifier ?? '一个任务'}`
  else if (type === 'comment') title = `${issue?.identifier ?? '一个任务'} 有新评论`
  return {
    id: `inbox-${i + 1}`,
    workspaceId: workspace.id,
    type,
    title,
    body: pick(DESC_SENTENCES),
    actorId: faker.helpers.arrayElement(allActorIds),
    issueId: issue?.id ?? null,
    read: faker.datatype.boolean({ probability: 0.4 }),
    createdAt: faker.date.recent({ days: 10 }).toISOString(),
  }
})

export const runtimes: Runtime[] = Array.from({ length: 6 }, (_, i) => ({
  id: `runtime-${i + 1}`,
  workspaceId: workspace.id,
  name: `runtime-${faker.word.adjective()}-${i + 1}`,
  type: faker.helpers.arrayElement(['sandbox', 'container', 'vm']),
  status: faker.helpers.arrayElement(['running', 'running', 'stopped', 'provisioning']),
  region: faker.helpers.arrayElement(['us-east-1', 'us-west-2', 'eu-west-1', 'ap-southeast-1']),
  cpu: faker.helpers.arrayElement([1, 2, 4, 8]),
  memoryGb: faker.helpers.arrayElement([2, 4, 8, 16]),
  createdAt: faker.date.past({ years: 1 }).toISOString(),
}))

export const invoices: Invoice[] = Array.from({ length: 6 }, (_, i) => ({
  id: `invoice-${i + 1}`,
  workspaceId: workspace.id,
  date: faker.date.past({ years: 1 }).toISOString(),
  amount: faker.number.int({ min: 49, max: 899 }),
  status: i === 0 ? 'pending' : 'paid',
}))

export function actorById(id: string | null): User | Agent | undefined {
  if (!id) return undefined
  return [...users, ...agents].find((a) => a.id === id)
}
