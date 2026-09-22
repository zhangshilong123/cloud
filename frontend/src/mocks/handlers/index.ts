import { agentHandlers } from './agents'
import { authHandlers } from './auth'
import { billingHandlers } from './billing'
import { chatHandlers } from './chat'
import { inboxHandlers } from './inbox'
import { issueHandlers } from './issues'
import { projectHandlers } from './projects'
import { runtimeHandlers } from './runtimes'
import { skillHandlers } from './skills'
import { squadHandlers } from './squads'
import { workspaceHandlers } from './workspaces'

export const handlers = [
  ...workspaceHandlers,
  ...authHandlers,
  ...issueHandlers,
  ...projectHandlers,
  ...squadHandlers,
  ...agentHandlers,
  ...chatHandlers,
  ...inboxHandlers,
  ...skillHandlers,
  ...runtimeHandlers,
  ...billingHandlers,
]
