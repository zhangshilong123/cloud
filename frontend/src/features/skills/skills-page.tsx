import { format } from 'date-fns'
import { Sparkles } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import type { Skill } from '@/api/generated.schemas'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ImportSkillDialog } from '@/features/skills/import-skill-dialog'
import { useSkills } from '@/features/skills/api'
import { formatBytes } from '@/features/skills/present'
import { useCurrentSpace } from '@/features/spaces/current-space'
import { workspacePaths } from '@/lib/paths'

const SKELETON_KEYS = ['one', 'two', 'three', 'four', 'five']

/**
 * Live Skills of the current space, cursor-paginated. Each row links to the
 * Skill detail and shows its identity, the current revision's package
 * description (falling back to the workspace summary) and size — never the
 * content digest, which is machine metadata. The "导入技能" action opens the
 * import dialog against the real import API.
 */
export function SkillsPage({ slug }: { slug: string }) {
  const skills = useSkills(slug)
  const p = workspacePaths(slug)
  const { space } = useCurrentSpace()
  const ready = space?.slug === slug
  const [importOpen, setImportOpen] = useState(false)
  const items = skills.data?.pages.flatMap((page) => page.items) ?? []

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="技能"
        actions={
          ready && (
            <Button size="sm" onClick={() => setImportOpen(true)}>
              导入技能
            </Button>
          )
        }
      />
      {ready && <ImportSkillDialog open={importOpen} onOpenChange={setImportOpen} />}
      <div className="flex-1 overflow-y-auto">
        {skills.isPending && (
          <div className="space-y-2 p-4">
            {SKELETON_KEYS.map((key) => (
              <Skeleton key={key} className="h-16 w-full" />
            ))}
          </div>
        )}
        {skills.isError && <p className="p-4 text-sm text-destructive">加载技能失败。</p>}
        {!skills.isPending && !skills.isError && items.length === 0 && (
          <p className="p-4 text-sm text-muted-foreground">暂无技能。导入一个 Skill 开始。</p>
        )}
        {items.map((skill) => (
          <SkillRow key={skill.id} skill={skill} to={p.skillDetail(skill.id)} />
        ))}
        {skills.hasNextPage && (
          <div className="p-4">
            <Button
              variant="outline"
              className="w-full"
              disabled={skills.isFetchingNextPage}
              onClick={() => void skills.fetchNextPage()}
            >
              {skills.isFetchingNextPage ? '加载中…' : '加载更多'}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

/** One Skill row: identity plus description on the left, current-revision size and update time on the right. */
function SkillRow({ skill, to }: { skill: Skill; to: string }) {
  return (
    <Link to={to} className="flex items-center gap-3 border-b px-4 py-3 hover:bg-muted/50">
      <Sparkles className="size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <p className="truncate text-sm font-medium">{skill.displayName}</p>
          <Badge variant="outline">{skill.canonicalName}</Badge>
          <Badge variant="secondary">v{skill.version}</Badge>
        </div>
        <p className="truncate text-xs text-muted-foreground">
          {skill.currentRevision?.description || skill.summary}
        </p>
      </div>
      <div className="hidden shrink-0 text-right text-xs text-muted-foreground sm:block">
        {skill.currentRevision ? (
          <p>{formatBytes(skill.currentRevision.sizeBytes)}</p>
        ) : (
          <p>暂无版本</p>
        )}
      </div>
      <span className="hidden shrink-0 text-xs text-muted-foreground md:inline">
        {format(new Date(skill.updatedAt), 'M月d日')}
      </span>
    </Link>
  )
}
