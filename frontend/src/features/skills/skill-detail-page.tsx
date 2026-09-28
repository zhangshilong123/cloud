import { format } from 'date-fns'
import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import type { SkillRevision } from '@/api/generated.schemas'
import { PageHeader } from '@/components/layout/page-header'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ImportSkillDialog } from '@/features/skills/import-skill-dialog'
import { useDeleteSkill, useSkill } from '@/features/skills/api'
import { formatBytes } from '@/features/skills/present'
import { workspacePaths } from '@/lib/paths'

/**
 * One Skill's public identity and current revision. The package `description`
 * (authored in SKILL.md) leads the body; the workspace `summary` follows as
 * secondary product metadata. The content digest is machine metadata and is
 * deliberately not rendered, and no storage locator or signed URL is ever
 * shown. "上传新版本" reuses the import dialog with the Skill id as
 * `target_skill_id`, so a new immutable revision is appended rather than the
 * Skill being overwritten.
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
          <div className="flex items-center gap-2">
            <Button size="sm" onClick={() => setUploadOpen(true)}>
              上传新版本
            </Button>
            <DeleteSkillButton
              slug={slug}
              skillId={skill.id}
              skillTitle={skill.displayName}
              version={skill.version}
            />
          </div>
        }
      />
      <ImportSkillDialog open={uploadOpen} onOpenChange={setUploadOpen} targetSkillId={skill.id} />
      <div className="flex-1 overflow-y-auto p-6">
        <div className="mb-6 space-y-2">
          <div className="flex items-center gap-2">
            <Badge variant="outline">{skill.canonicalName}</Badge>
            <Badge variant="secondary">v{skill.version}</Badge>
          </div>
          <p className="text-sm text-foreground">
            {skill.currentRevision?.description || '暂无描述。'}
          </p>
          {skill.summary && skill.summary !== skill.currentRevision?.description && (
            <p className="text-xs text-muted-foreground">工作区说明：{skill.summary}</p>
          )}
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

/** The current (immutable) revision: revision id, size and package format — no content digest. */
function CurrentRevision({ revision }: { revision: SkillRevision }) {
  return (
    <div className="space-y-3 rounded-lg border p-4 text-sm">
      <RevisionField label="Revision ID" mono value={revision.id} />
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

/** Delete confirmation: soft-deletes the Skill and returns to the list on success. */
function DeleteSkillButton({
  slug,
  skillId,
  skillTitle,
  version,
}: {
  slug: string
  skillId: string
  skillTitle: string
  version: number
}) {
  const navigate = useNavigate()
  const p = workspacePaths(slug)
  const deleteSkill = useDeleteSkill()

  async function confirmDelete() {
    try {
      await deleteSkill.mutateAsync({ id: skillId, version })
      void navigate(p.skills)
    } catch {
      // the error code is rendered by the dialog
    }
  }

  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={
          <Button size="sm" variant="destructive">
            删除技能
          </Button>
        }
      />
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>删除「{skillTitle}」？</AlertDialogTitle>
          <AlertDialogDescription>
            技能将从列表移除，但已冻结的 Execution 与历史版本仍保留。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction onClick={() => void confirmDelete()}>确认删除</AlertDialogAction>
        </AlertDialogFooter>
        {deleteSkill.error?.response?.data?.code && (
          <p className="text-xs text-destructive">
            删除失败：{deleteSkill.error?.response?.data?.code}
          </p>
        )}
      </AlertDialogContent>
    </AlertDialog>
  )
}
