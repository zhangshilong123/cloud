import { format } from 'date-fns'
import { Bot, Plus } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import type { Agent } from '@/api/generated.schemas'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { CreateAgentDialog } from '@/features/agents/create-agent-dialog'
import { useAgents } from '@/features/agents/api'
import { AGENT_STATUS_LABELS } from '@/features/agents/status'
import { useCurrentSpace } from '@/features/spaces/current-space'
import { workspacePaths } from '@/lib/paths'

const SKELETON_KEYS = ['one', 'two', 'three', 'four', 'five', 'six']

/**
 * Live Agents of the current space, cursor-paginated. Each card links to the
 * Agent detail (bindings + execution demo); creation is via the dialog against
 * the real Agent API.
 */
export function AgentsPage({ slug }: { slug: string }) {
  const agents = useAgents(slug)
  const p = workspacePaths(slug)
  const { space } = useCurrentSpace()
  const ready = space?.slug === slug
  const [createOpen, setCreateOpen] = useState(false)
  const items = agents.data?.pages.flatMap((page) => page.items) ?? []

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="智能体"
        actions={
          ready && (
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" />
              新建智能体
            </Button>
          )
        }
      />
      {ready && <CreateAgentDialog open={createOpen} onOpenChange={setCreateOpen} />}
      <div className="flex-1 overflow-y-auto p-4">
        {agents.isPending && (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {SKELETON_KEYS.map((key) => (
              <Skeleton key={key} className="h-20 w-full" />
            ))}
          </div>
        )}
        {agents.isError && <p className="text-sm text-destructive">加载智能体失败。</p>}
        {!agents.isPending && !agents.isError && items.length === 0 && (
          <p className="text-sm text-muted-foreground">暂无智能体。新建一个 Agent 开始。</p>
        )}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {items.map((agent) => (
            <AgentCard key={agent.id} agent={agent} to={p.agentDetail(agent.id)} />
          ))}
        </div>
        {agents.hasNextPage && (
          <Button
            variant="outline"
            className="mt-4 w-full"
            disabled={agents.isFetchingNextPage}
            onClick={() => void agents.fetchNextPage()}
          >
            {agents.isFetchingNextPage ? '加载中…' : '加载更多'}
          </Button>
        )}
      </div>
    </div>
  )
}

/** One Agent card: bot glyph, name, lifecycle badge and creation time. */
function AgentCard({ agent, to }: { agent: Agent; to: string }) {
  return (
    <Link to={to} className="flex items-center gap-3 rounded-lg border p-4 hover:bg-muted/50">
      <Bot className="size-8 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">{agent.name}</p>
        <p className="mt-1 text-xs text-muted-foreground">
          {format(new Date(agent.createdAt), 'yyyy年M月d日')}
        </p>
      </div>
      <Badge variant={agent.status === 'active' ? 'secondary' : 'outline'}>
        {AGENT_STATUS_LABELS[agent.status]}
      </Badge>
    </Link>
  )
}
