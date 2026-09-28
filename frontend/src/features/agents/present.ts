import type { AttemptState } from '@/api/generated.schemas'
import { formatBytes, shortDigest } from '@/features/skills/present'

export { formatBytes, shortDigest }

/**
 * Product wording for the attempt lifecycle. `eligible` is what a freshly
 * admitted Execution really is — the runtime has not delivered it — so the UI
 * words it as "等待运行时投递" instead of pretending it is running or done.
 */
export const ATTEMPT_STATE_LABELS: Record<AttemptState, string> = {
  eligible: '等待运行时投递',
  dispatched: '已派发',
  running: '运行中',
  succeeded: '成功',
  failed: '失败',
  canceled: '已取消',
  superseded: '已替代',
}
