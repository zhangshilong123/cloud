import { useState } from 'react'
import { useParams } from 'react-router-dom'
import type { AgentSkillBindingListItem } from '@/api/generated.schemas'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  useAgent,
  useAgentSkillBindings,
  useCreateExecution,
  useDetachSkill,
  useSetSkillEnabled,
  useUpdateAgent,
} from '@/features/agents/api'
import { AttachSkillDialog } from '@/features/agents/attach-skill-dialog'
import { ExecutionSnapshot } from '@/features/agents/execution-snapshot'
import { AGENT_STATUS_LABELS } from '@/features/agents/status'
import { workspacePaths } from '@/lib/paths'

/**
 * Demo main page for one Agent: its identity and status, its assigned Skill
 * bindings (attach / enable-disable / detach, all against the real Agent Skill
 * API) and the Execution Demo (admit with agent identity only, then inspect the
 * frozen snapshot from GET /executions/:executionId).
 */
export function AgentDetailPage({ slug }: { slug: string }) {
  const { agentId } = useParams<{ agentId: string }>()
  const { data: agent, isPending, isError } = useAgent(slug, agentId)
  const bindings = useAgentSkillBindings(slug, agentId)
  const updateAgent = useUpdateAgent()
  const p = workspacePaths(slug)
  const [attachOpen, setAttachOpen] = useState(false)

  const breadcrumb = { label: '智能体', to: p.agents }

  if (isError) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader title="智能体" breadcrumb={breadcrumb} />
        <p className="p-6 text-sm text-destructive">加载智能体失败或不存在。</p>
      </div>
    )
  }

  if (isPending || !agent) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader title="智能体" breadcrumb={breadcrumb} />
        <div className="space-y-3 p-6">
          <Skeleton className="h-6 w-1/2" />
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-32 w-full" />
        </div>
      </div>
    )
  }

  const bindingItems = bindings.data?.items ?? []
  const boundIds = new Set(bindingItems.map((binding) => binding.skillId))

  return (
    <div className="flex h-full flex-col">
      <PageHeader title={agent.name} breadcrumb={breadcrumb} />
      <AttachSkillDialog
        open={attachOpen}
        onOpenChange={setAttachOpen}
        slug={slug}
        agentId={agent.id}
        excludeIds={boundIds}
      />
      <div className="mx-auto w-full max-w-3xl flex-1 space-y-6 overflow-y-auto p-6">
        <section className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Badge variant={agent.status === 'active' ? 'secondary' : 'outline'}>
              {AGENT_STATUS_LABELS[agent.status]}
            </Badge>
            <Badge variant="outline">v{agent.version}</Badge>
          </div>
          <Label className="gap-2 text-sm font-normal">
            <Switch
              checked={agent.status === 'active'}
              onCheckedChange={(checked) =>
                updateAgent.mutate({
                  id: agent.id,
                  version: agent.version,
                  status: checked ? 'active' : 'disabled',
                })
              }
              disabled={updateAgent.isPending}
            />
            {agent.status === 'active' ? '已启用' : '已禁用'}
          </Label>
        </section>

        <section className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold">Assigned Skills（{bindingItems.length}）</h2>
            <Button size="sm" variant="outline" onClick={() => setAttachOpen(true)}>
              绑定 Skill
            </Button>
          </div>
          {bindings.isPending && <Skeleton className="h-20 w-full" />}
          {!bindings.isPending && bindingItems.length === 0 && (
            <p className="text-sm text-muted-foreground">
              暂无绑定。绑定一个 Skill 后即可演示 Execution。
            </p>
          )}
          {!bindings.isPending && bindingItems.length > 0 && (
            <ul className="space-y-1.5">
              {bindingItems.map((binding) => (
                <BindingRow key={binding.id} agentId={agent.id} binding={binding} />
              ))}
            </ul>
          )}
        </section>

        <section className="space-y-3">
          <h2 className="text-sm font-semibold">Execution Demo</h2>
          <ExecutionDemo slug={slug} agentId={agent.id} />
        </section>
      </div>
    </div>
  )
}

/** One binding: enable-disable (version-guarded PUT) and detach (DELETE). */
function BindingRow({ agentId, binding }: { agentId: string; binding: AgentSkillBindingListItem }) {
  const setEnabled = useSetSkillEnabled()
  const detach = useDetachSkill()

  return (
    <li className="flex items-center gap-3 rounded-md border px-3 py-2 text-sm">
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium">{binding.displayName || binding.canonicalName}</p>
        <p className="truncate font-mono text-xs text-muted-foreground">{binding.canonicalName}</p>
      </div>
      <Label className="gap-2 text-xs font-normal">
        <Switch
          checked={binding.enabled}
          onCheckedChange={(checked) =>
            setEnabled.mutate({
              agentId,
              skillId: binding.skillId,
              enabled: checked,
              version: binding.version,
            })
          }
          disabled={setEnabled.isPending}
        />
        启用
      </Label>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => detach.mutate({ agentId, skillId: binding.skillId })}
        disabled={detach.isPending}
      >
        解除绑定
      </Button>
    </li>
  )
}

/**
 * Admits an Execution carrying only the Agent identity (the Skill set is
 * resolved server-side from durable bindings). One stable Idempotency-Key per
 * user action: retry reuses the same variables object, and a fresh key is
 * minted only by the explicit "创建另一个 Execution" action.
 */
function ExecutionDemo({ slug, agentId }: { slug: string; agentId: string }) {
  const createExecution = useCreateExecution()
  const [admission, setAdmission] = useState<{ agentId: string } | null>(null)
  const [executionId, setExecutionId] = useState<string>()

  function admit(next: { agentId: string }) {
    setAdmission(next)
    createExecution.mutate(next, {
      onSuccess: (record) => setExecutionId(record.execution.executionId),
    })
  }

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        提交时 body 仅携带 Agent 身份；Skill 集合由服务端从持久化绑定中解析并冻结。重试复用同一
        Idempotency-Key，只有“创建另一个 Execution”才会生成新 Key。
      </p>
      <div className="flex flex-wrap items-center gap-2">
        {!executionId ? (
          <Button onClick={() => admit({ agentId })} disabled={createExecution.isPending}>
            {createExecution.isPending ? '提交中…' : '创建 Execution'}
          </Button>
        ) : (
          <Button
            variant="outline"
            onClick={() => admit({ agentId })}
            disabled={createExecution.isPending}
          >
            创建另一个 Execution
          </Button>
        )}
        {createExecution.isError && admission && (
          <Button
            variant="outline"
            onClick={() => admit(admission)}
            disabled={createExecution.isPending}
          >
            重试
          </Button>
        )}
      </div>
      {createExecution.isError && (
        <p className="text-xs text-destructive">
          提交失败：{createExecution.error.response?.data?.code}
        </p>
      )}
      {executionId && <ExecutionSnapshot slug={slug} executionId={executionId} />}
    </div>
  )
}
