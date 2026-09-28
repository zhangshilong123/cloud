import { billingHandlers } from './billing'
import { chatHandlers } from './chat'
import { inboxHandlers } from './inbox'
import { issueHandlers } from './issues'
import { projectHandlers } from './projects'
import { runtimeHandlers } from './runtimes'
import { squadHandlers } from './squads'
import { workspaceHandlers } from './workspaces'

export const handlers = [
  ...workspaceHandlers,
  ...issueHandlers,
  ...projectHandlers,
  ...squadHandlers,
  ...chatHandlers,
  ...inboxHandlers,
  ...runtimeHandlers,
  ...billingHandlers,
]
