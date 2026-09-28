import type { AgentStatus } from '@/api/generated.schemas'

/** Product labels for the cloud Agent lifecycle (`active`/`disabled`). */
export const AGENT_STATUS_LABELS: Record<AgentStatus, string> = {
  active: '已启用',
  disabled: '已禁用',
}
