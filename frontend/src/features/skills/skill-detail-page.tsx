import { format } from 'date-fns'
import { useState } from 'react'
import { useParams } from 'react-router-dom'
import type { SkillRevision } from '@/api/generated.schemas'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ImportSkillDialog } from '@/features/skills/import-skill-dialog'
import { useSkill } from '@/features/skills/api'
import { formatBytes } from '@/features/skills/present'
import { workspacePaths } from '@/lib/paths'

/**
 * One Skill's public identity and current revision. The current revision shows
 * the full content digest (selectable, so it can be copied) and never any
 * storage locator or signed URL. "上传新版本" reuses the import dialog with the
 * Skill id as `target_skill_id`, so a new immutable revision is appended rather
 * than the Skill being overwritten.
 */
export function SkillDetailPage({ slug }: { slug: string }) {
  const { skillId } = useParams<{ skillId: string }>()
  const { data: skill, isPending, isError } = useSkill(slug, skillId)
  const p = workspacePaths(slug)
  const [uploadOpen, setUploadOpen] = useState(false)

  if (isError) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader title="技能" breadcrumb={{ label: '技能', to: p.skills }} />
        <p className="p-6 text-sm text-destructive">加载技能失败或不存在。</p>
      </div>
    )
  }

  if (isPending || !skill) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader title="技能" breadcrumb={{ label: '技能', to: p.skills }} />
        <div className="space-y-3 p-6">
          <Skeleton className="h-6 w-1/2" />
          <Skeleton className="h-24 w-full" />
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={skill.displayName}
        breadcrumb={{ label: '技能', to: p.skills }}
        actions={
          <Button size="sm" onClick={() => setUploadOpen(true)}>
            上传新版本
          </Button>
        }
      />
      <ImportSkillDialog open={uploadOpen} onOpenChange={setUploadOpen} targetSkillId={skill.id} />
      <div className="flex-1 overflow-y-auto p-6">
        <div className="mb-6 space-y-2">
          <div className="flex items-center gap-2">
            <Badge variant="outline">{skill.canonicalName}</Badge>
            <Badge variant="secondary">v{skill.version}</Badge>
          </div>
          <p className="text-sm text-muted-foreground">{skill.summary || '暂无描述。'}</p>
          <p className="text-xs text-muted-foreground">
            创建于 {format(new Date(skill.createdAt), 'yyyy年M月d日')} · 更新于{' '}
            {format(new Date(skill.updatedAt), 'yyyy年M月d日')}
          </p>
        </div>

        <h2 className="mb-2 text-xs font-medium text-muted-foreground">当前版本</h2>
        {skill.currentRevision ? (
          <CurrentRevision revision={skill.currentRevision} />
        ) : (
          <p className="text-sm text-muted-foreground">暂无版本。</p>
        )}
      </div>
    </div>
  )
}

/** The current (immutable) revision: revision id, full digest, size and package format. */
function CurrentRevision({ revision }: { revision: SkillRevision }) {
  return (
    <div className="space-y-3 rounded-lg border p-4 text-sm">
      <RevisionField label="Revision ID" mono value={revision.id} />
      <RevisionField label="内容摘要" mono value={revision.contentDigest} />
      <RevisionField label="大小" value={formatBytes(revision.sizeBytes)} />
      <RevisionField
        label="包格式"
        value={`${revision.packageFormat} v${revision.packageFormatVersion}`}
      />
    </div>
  )
}

function RevisionField({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex flex-col gap-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={mono ? 'select-all break-all font-mono text-xs' : 'text-sm text-foreground'}>
        {value}
      </dd>
    </div>
  )
}
