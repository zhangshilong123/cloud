import { format } from 'date-fns'
import { RefreshCw, Snowflake } from 'lucide-react'
import type { Attempt, ExecutionSkillBinding } from '@/api/generated.schemas'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useExecution } from '@/features/agents/api'
import { ATTEMPT_STATE_LABELS, formatBytes, shortDigest } from '@/features/agents/present'

/**
 * Frozen-snapshot inspector for one admitted Execution. Snapshot authority is
 * GET /executions/:executionId only: nothing here merges the frozen bindings
 * with the live Skills list or fakes a lifecycle. Attempts render their real
 * state (a fresh admission is `eligible` → "等待运行时投递"), and the binding
 * rows are the immutable revision set captured at admission time.
 */
export function ExecutionSnapshot({ slug, executionId }: { slug: string; executionId: string }) {
  const execution = useExecution(slug, executionId)
  const record = execution.data

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">
          {execution.isFetching ? '刷新中…' : '快照来自 GET /executions/:executionId'}
        </span>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void execution.refetch()}
          disabled={execution.isFetching}
        >
          <RefreshCw className="size-3.5" />
          刷新快照
        </Button>
      </div>

      {execution.isError && <p className="text-sm text-destructive">加载 Execution 快照失败。</p>}
      {execution.isPending && <Skeleton className="h-48 w-full" />}
      {!execution.isPending && !execution.isError && record && (
        <>
          <div className="flex items-center gap-2 rounded-md border border-dashed px-3 py-2 text-xs">
            <Snowflake className="size-4 shrink-0 text-sky-500" />
            <span className="font-medium">此 Execution 已冻结 · Frozen for this execution</span>
          </div>
          <dl className="rounded-lg border p-4 text-sm">
            <Field label="Execution ID" value={record.execution.executionId} mono />
            <Field label="Agent ID" value={record.execution.agentId} mono />
            <Field
              label="创建于"
              value={format(new Date(record.execution.createdAt), 'yyyy年M月d日 HH:mm:ss')}
            />
          </dl>
          <Attempts attempts={record.attempts} />
          <FrozenBindings bindings={record.skillBindings} />
        </>
      )}
    </div>
  )
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex flex-col gap-0.5 py-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={mono ? 'select-all break-all font-mono text-xs' : 'text-sm'}>{value}</dd>
    </div>
  )
}

/** The attempt lifecycle, verbatim from the frozen aggregate — real states only. */
function Attempts({ attempts }: { attempts: Attempt[] }) {
  if (attempts.length === 0) {
    return <p className="text-sm text-muted-foreground">暂无 Attempt。</p>
  }
  return (
    <section className="space-y-2">
      <h3 className="text-xs font-medium text-muted-foreground">
        尝试（Attempts，{attempts.length}）
      </h3>
      <ul className="space-y-1.5">
        {attempts.map((attempt) => (
          <li
            key={attempt.attemptId}
            className="flex items-center gap-2 rounded-md border px-3 py-2 text-sm"
          >
            <span className="font-mono text-xs text-muted-foreground">#{attempt.ordinal}</span>
            <Badge variant="outline">{ATTEMPT_STATE_LABELS[attempt.state]}</Badge>
            <span className="ml-auto text-xs text-muted-foreground">
              {format(new Date(attempt.createdAt), 'HH:mm:ss')}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

/** The immutable skill set snapshotted at admission — never joined with the live list. */
function FrozenBindings({ bindings }: { bindings: ExecutionSkillBinding[] }) {
  if (bindings.length === 0) {
    return <p className="text-sm text-muted-foreground">该 Execution 未冻结任何 Skill 绑定。</p>
  }
  return (
    <section className="space-y-2">
      <h3 className="text-xs font-medium text-muted-foreground">
        已冻结的 Skill（{bindings.length}）
      </h3>
      <ul className="space-y-1.5">
        {bindings.map((binding) => (
          <li key={binding.skillId} className="rounded-md border px-3 py-2 text-sm">
            <div className="flex items-center gap-2">
              <span className="font-medium">{binding.canonicalName}</span>
              <Badge variant="outline">
                {binding.packageFormat} v{binding.packageFormatVersion}
              </Badge>
              <span className="ml-auto text-xs text-muted-foreground">
                {formatBytes(binding.sizeBytes)}
              </span>
            </div>
            <p
              className="mt-1 select-all break-all font-mono text-xs text-muted-foreground"
              title={binding.contentDigest}
            >
              {shortDigest(binding.contentDigest)}
            </p>
          </li>
        ))}
      </ul>
    </section>
  )
}
