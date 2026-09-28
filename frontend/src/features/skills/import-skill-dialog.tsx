import { useRef, useState } from 'react'
import type { ChangeEvent, FormEvent } from 'react'
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
import { folderToZip } from '@/features/skills/folder-zip'
import { formatBytes } from '@/features/skills/present'

type SourceKind = 'zip' | 'tar'
type SourceMode = 'archive' | 'folder'

/** A folder pick in progress or resolved; `undefined` means nothing picked yet. */
type FolderPick =
  | { status: 'ready'; file: File; entryCount: number }
  | { status: 'packaging' }
  | { status: 'empty' }
  | { status: 'oversize'; budgetBytes: number }

/** The archive to submit: the picked archive, or the folder's packaged ZIP. */
function resolveSourceFile(
  mode: SourceMode,
  file: File | undefined,
  folder: FolderPick | undefined,
): File | undefined {
  if (mode === 'archive') return file
  return folder?.status === 'ready' ? folder.file : undefined
}

/** Human label for the folder picker button, reflecting the pick lifecycle. */
function folderButtonLabel(folder: FolderPick | undefined): string {
  if (folder?.status === 'packaging') return '打包中…'
  if (folder?.status === 'ready') return '重新选择文件夹'
  return '选择文件夹'
}

/**
 * Opens the import form for a new Skill (no `targetSkillId`) or for a new
 * revision of an existing Skill (`targetSkillId` set). The source is either one
 * archive (ZIP / uncompressed TAR) or a directory the browser packages into one
 * ZIP for transport — the backend stays the sole authority for Skill discovery.
 * The hook's per-submission Idempotency-Key makes a retry resume instead of
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
            归档文件（或整个文件夹，会在浏览器内打包为单个 ZIP）会被解码为确定性候选并进入 ingest
            流程；同一提交的重试复用同一 Idempotency-Key。
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

/** The upload form: display name, then either one archive or one picked folder. */
function ImportForm({
  importSkill,
  targetSkillId,
  onImported,
}: {
  importSkill: ReturnType<typeof useImportSkill>
  targetSkillId: string | undefined
  onImported: (result: SourceUploadResult) => void
}) {
  const [mode, setMode] = useState<SourceMode>('archive')
  const [sourceKind, setSourceKind] = useState<SourceKind>('zip')
  const [file, setFile] = useState<File>()
  const [folder, setFolder] = useState<FolderPick>()
  const [displayName, setDisplayName] = useState('')

  const sourceFile = resolveSourceFile(mode, file, folder)
  const submittable = sourceFile !== undefined && !importSkill.isPending
  const errorCode = importSkill.error?.response?.data?.code

  function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!submittable || !sourceFile) return
    void (async () => {
      try {
        const value = await importSkill.mutateAsync({
          sourceKind: mode === 'archive' ? sourceKind : 'zip',
          source: sourceFile,
          ...(displayName.trim() ? { displayName: displayName.trim() } : {}),
          ...(targetSkillId ? { targetSkillId } : {}),
        })
        onImported(value)
      } catch {
        // the hook surfaces the fault code next to the form
      }
    })()
  }

  return (
    <form onSubmit={handleSubmit} noValidate className="space-y-4">
      {!targetSkillId && (
        <DialogFormField
          id="import-skill-name"
          label="显示名称（可选）"
          value={displayName}
          onChange={setDisplayName}
          placeholder="Demo Skill"
        />
      )}
      <SourceModeToggle mode={mode} onModeChange={setMode} />
      {mode === 'archive' ? (
        <ArchiveSourceField
          sourceKind={sourceKind}
          onSourceKindChange={setSourceKind}
          onFileChange={setFile}
        />
      ) : (
        <FolderSourceField folder={folder} onFolderChange={setFolder} />
      )}
      {errorCode && <p className="text-xs text-destructive">导入失败：{errorCode}</p>}
      <Button type="submit" className="w-full" disabled={!submittable}>
        {importSkill.isPending ? '导入中…' : '导入'}
      </Button>
    </form>
  )
}

/** Two-way switch between uploading one archive and picking a whole folder. */
function SourceModeToggle({
  mode,
  onModeChange,
}: {
  mode: SourceMode
  onModeChange: (mode: SourceMode) => void
}) {
  return (
    <div className="space-y-1.5">
      <Label>导入方式</Label>
      <div className="flex gap-2">
        <Button
          type="button"
          variant={mode === 'archive' ? 'default' : 'outline'}
          size="sm"
          onClick={() => onModeChange('archive')}
        >
          归档文件
        </Button>
        <Button
          type="button"
          variant={mode === 'folder' ? 'default' : 'outline'}
          size="sm"
          onClick={() => onModeChange('folder')}
        >
          文件夹
        </Button>
      </div>
    </div>
  )
}

/** One archive pick: format selector plus a `.zip`/`.tar` file input. */
function ArchiveSourceField({
  sourceKind,
  onSourceKindChange,
  onFileChange,
}: {
  sourceKind: SourceKind
  onSourceKindChange: (kind: SourceKind) => void
  onFileChange: (file: File | undefined) => void
}) {
  return (
    <>
      <div className="space-y-1.5">
        <Label>归档格式</Label>
        <Select value={sourceKind} onValueChange={(value) => onSourceKindChange(value ?? 'zip')}>
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
          onChange={(e) => onFileChange(e.target.files?.[0])}
        />
      </div>
    </>
  )
}

/** One folder pick: a `webkitdirectory` input packaged into a ZIP for transport. */
function FolderSourceField({
  folder,
  onFolderChange,
}: {
  folder: FolderPick | undefined
  onFolderChange: (folder: FolderPick) => void
}) {
  const folderInputRef = useRef<HTMLInputElement | null>(null)

  async function handleFolderChange(e: ChangeEvent<HTMLInputElement>) {
    // Snapshot the live FileList into an array BEFORE resetting the input: in Chromium
    // `HTMLInputElement.files` is live, so clearing `value` empties a previously captured
    // reference and would make a non-empty folder look empty.
    const files = Array.from(e.target.files ?? [])
    e.target.value = ''
    if (files.length === 0) {
      onFolderChange({ status: 'empty' })
      return
    }
    onFolderChange({ status: 'packaging' })
    const built = await folderToZip(files)
    if (built.kind === 'ok') {
      onFolderChange({ status: 'ready', file: built.file, entryCount: built.entryCount })
    } else if (built.kind === 'empty') {
      onFolderChange({ status: 'empty' })
    } else {
      onFolderChange({ status: 'oversize', budgetBytes: built.budgetBytes })
    }
  }

  return (
    <div className="space-y-1.5">
      <Label>文件夹</Label>
      {folder?.status === 'ready' && (
        <p className="text-xs text-muted-foreground">
          {folder.entryCount} 个文件 · 将打包为单个 ZIP 上传
        </p>
      )}
      {folder?.status === 'empty' && (
        <p className="text-xs text-destructive">所选文件夹为空，请选择包含文件的文件夹。</p>
      )}
      {folder?.status === 'oversize' && (
        <p className="text-xs text-destructive">
          所选文件夹超过 {formatBytes(folder.budgetBytes)} 上限，无法导入。
        </p>
      )}
      <Button
        type="button"
        variant="outline"
        className="w-full"
        disabled={folder?.status === 'packaging'}
        onClick={() => folderInputRef.current?.click()}
      >
        {folderButtonLabel(folder)}
      </Button>
      <input
        type="file"
        className="hidden"
        aria-label="选择文件夹"
        ref={(node) => {
          folderInputRef.current = node
          if (node) node.setAttribute('webkitdirectory', '')
        }}
        onChange={handleFolderChange}
      />
    </div>
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
