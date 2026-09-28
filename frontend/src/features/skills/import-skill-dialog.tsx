import { useState } from 'react'
import type { SourceUploadResult } from '@/api/generated.schemas'
import { DialogFormField } from '@/components/common/dialog-form-field'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useImportSkill } from '@/features/skills/api'

type SourceKind = 'zip' | 'tar'

/**
 * Opens the import form for a new Skill (no `targetSkillId`) or for a new
 * revision of an existing Skill (`targetSkillId` set). The reuse of the
 * hook's per-submission Idempotency-Key is what makes a retry resume instead of
 * importing twice. Results stay in the dialog so partial success and per-item
 * conflicts are visible before closing.
 */
export function ImportSkillDialog({
  open,
  onOpenChange,
  targetSkillId,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  targetSkillId?: string
}) {
  const importSkill = useImportSkill()
  const [result, setResult] = useState<SourceUploadResult>()

  function reset() {
    setResult(undefined)
    importSkill.reset()
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{targetSkillId ? '上传新版本' : '导入技能'}</DialogTitle>
          <DialogDescription>
            归档文件会被解码为确定性候选并进入 ingest 流程；同一提交的重试复用同一 Idempotency-Key。
          </DialogDescription>
        </DialogHeader>
        {result ? (
          <ImportResultSummary result={result} onClose={() => onOpenChange(false)} />
        ) : (
          <ImportForm
            importSkill={importSkill}
            targetSkillId={targetSkillId}
            onImported={setResult}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

/** The upload form: display name, source kind and one archive file. */
function ImportForm({
  importSkill,
  targetSkillId,
  onImported,
}: {
  importSkill: ReturnType<typeof useImportSkill>
  targetSkillId: string | undefined
  onImported: (result: SourceUploadResult) => void
}) {
  const [sourceKind, setSourceKind] = useState<SourceKind>('zip')
  const [file, setFile] = useState<File>()
  const [displayName, setDisplayName] = useState('')

  const submittable = file !== undefined && !importSkill.isPending
  const errorCode = importSkill.error?.response?.data?.code

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (!submittable || !file) return
        void (async () => {
          try {
            const value = await importSkill.mutateAsync({
              sourceKind,
              source: file,
              ...(displayName.trim() ? { displayName: displayName.trim() } : {}),
              ...(targetSkillId ? { targetSkillId } : {}),
            })
            onImported(value)
          } catch {
            // the hook surfaces the fault code next to the form
          }
        })()
      }}
      noValidate
      className="space-y-4"
    >
      {!targetSkillId && (
        <DialogFormField
          id="import-skill-name"
          label="显示名称（可选）"
          value={displayName}
          onChange={setDisplayName}
          placeholder="Demo Skill"
        />
      )}
      <div className="space-y-1.5">
        <Label>归档格式</Label>
        <Select value={sourceKind} onValueChange={(value) => setSourceKind(value ?? 'zip')}>
          <SelectTrigger className="w-full" aria-label="归档格式">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="zip">ZIP</SelectItem>
            <SelectItem value="tar">TAR（未压缩）</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="import-skill-file">归档文件</Label>
        <Input
          id="import-skill-file"
          type="file"
          accept=".zip,.tar"
          onChange={(e) => setFile(e.target.files?.[0])}
        />
      </div>
      {errorCode && <p className="text-xs text-destructive">导入失败：{errorCode}</p>}
      <Button type="submit" className="w-full" disabled={!submittable}>
        {importSkill.isPending ? '导入中…' : '导入'}
      </Button>
    </form>
  )
}

/**
 * Result of one import submission, distinguishing full success from partial
 * success (preparation failures and `activation_conflict` items) so the user
 * never mistakes a partial import for a clean one.
 */
function ImportResultSummary({
  result,
  onClose,
}: {
  result: SourceUploadResult
  onClose: () => void
}) {
  const activated = result.ingestions.filter((i) => i.activation === 'activated')
  const conflicts = result.ingestions.filter((i) => i.activation === 'activation_conflict')
  const isPartial = conflicts.length > 0 || result.preparationFailures.length > 0

  return (
    <div className="space-y-3">
      <p className="text-sm font-medium">
        {isPartial ? '部分成功' : '导入成功'}（{activated.length} 个已激活）
      </p>
      {conflicts.length > 0 && (
        <ul className="space-y-1 text-xs text-muted-foreground">
          {conflicts.map((item) => (
            <li key={item.canonicalName}>已存在同名 Skill（{item.canonicalName}），未更改</li>
          ))}
        </ul>
      )}
      {result.preparationFailures.length > 0 && (
        <ul className="space-y-1 text-xs text-destructive">
          {result.preparationFailures.map((failure) => (
            <li key={failure.candidateRoot}>
              {failure.candidateRoot}：{failure.errorCode}（{failure.detail}）
            </li>
          ))}
        </ul>
      )}
      <Button variant="outline" className="w-full" onClick={onClose}>
        完成
      </Button>
    </div>
  )
}
