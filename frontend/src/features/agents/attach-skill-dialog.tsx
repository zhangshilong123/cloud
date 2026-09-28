import { useState } from 'react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { useAttachSkill } from '@/features/agents/api'
import { useSkills } from '@/features/skills/api'

/**
 * Picks one already-imported Skill and attaches it to an Agent. Candidates
 * come from the live Skills list (GET /skills); skills already bound to this
 * Agent are filtered out so the attach is a clean create rather than a 409.
 */
export function AttachSkillDialog({
  open,
  onOpenChange,
  slug,
  agentId,
  excludeIds,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  slug: string
  agentId: string
  excludeIds?: ReadonlySet<string>
}) {
  const attachSkill = useAttachSkill()
  const skills = useSkills(slug)
  const [skillId, setSkillId] = useState('')

  const items = (skills.data?.pages.flatMap((page) => page.items) ?? []).filter(
    (skill) => !excludeIds?.has(skill.id),
  )
  const submittable = skillId !== '' && !attachSkill.isPending

  function reset() {
    setSkillId('')
    attachSkill.reset()
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>绑定 Skill</DialogTitle>
          <DialogDescription>选择一个已导入的 Skill 绑定到该 Agent。</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {skills.isPending && <Skeleton className="h-9 w-full" />}
          {!skills.isPending && items.length === 0 && (
            <p className="text-sm text-muted-foreground">
              没有可绑定的 Skill。请先在技能页导入一个 Skill。
            </p>
          )}
          {!skills.isPending && items.length > 0 && (
            <Select value={skillId} onValueChange={(value) => setSkillId(value ?? '')}>
              <SelectTrigger className="w-full" aria-label="选择 Skill">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {items.map((skill) => (
                  <SelectItem key={skill.id} value={skill.id}>
                    {skill.displayName || skill.canonicalName}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {attachSkill.error && (
            <p className="text-xs text-destructive">
              绑定失败：{attachSkill.error.response?.data?.code}
            </p>
          )}
          <Button
            className="w-full"
            disabled={!submittable}
            onClick={() => {
              if (!submittable) return
              attachSkill.mutate({ agentId, skillId }, { onSuccess: () => onOpenChange(false) })
            }}
          >
            {attachSkill.isPending ? '绑定中…' : '绑定'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
