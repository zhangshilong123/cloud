import { format } from 'date-fns'
import { useState } from 'react'
import { ActorAvatar } from '@/components/common/actor-avatar'
import { DialogFormField } from '@/components/common/dialog-form-field'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  useAddSpaceMemberByEmail,
  useMembers,
  useRemoveSpaceMember,
  type MemberWithUser,
} from '@/features/members/api'
import { normalizeSpaceRole, useUpdateSpaceMember, type SpaceRole } from '@/features/spaces/api'
import { useCurrentSpace } from '@/features/spaces/current-space'

const ROLE_LABELS: Record<string, string> = {
  owner: '所有者',
  admin: '管理员',
  member: '成员',
}

const STATUS_LABELS: Record<string, string> = {
  active: '已加入',
  invited: '待加入',
}
const SKELETON_KEYS = ['one', 'two', 'three', 'four', 'five']

/** Role options an actor may grant; only owners may grant owner. */
function roleOptions(canGrantOwner: boolean): SpaceRole[] {
  return canGrantOwner ? ['owner', 'admin', 'member'] : ['admin', 'member']
}

/**
 * Members page. Cloud sessions render the real membership list with
 * admin/owner management controls (role changes, disable/enable, add member by
 * email, owner-only removal); mock sessions keep the demo store table.
 */
export function MembersPage({ slug }: { slug: string }) {
  const { data: members, isPending } = useMembers(slug)
  const { tenantId, space } = useCurrentSpace()
  const cloudSpace = space?.slug === slug ? space : undefined

  if (cloudSpace) {
    return (
      <CloudMembersView
        tenantId={tenantId ?? ''}
        spaceId={cloudSpace.id}
        members={members ?? []}
        isPending={isPending}
        myRole={normalizeSpaceRole(cloudSpace.role)}
      />
    )
  }

  return (
    <div className="p-4">
      {isPending && (
        <div className="space-y-2">
          {SKELETON_KEYS.map((key) => (
            <Skeleton key={key} className="h-10 w-full" />
          ))}
        </div>
      )}
      {members && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>成员</TableHead>
              <TableHead>角色</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>加入时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {members.map((member) => (
              <MemberCells key={member.id} member={member} />
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}

/** Read-only member row for the mock store. */
function MemberCells({ member }: { member: MemberWithUser }) {
  return (
    <TableRow>
      <TableCell>
        <div className="flex items-center gap-2">
          <ActorAvatar actor={member} size="sm" />
          <div>
            <p className="text-sm font-medium">{member.name}</p>
            <p className="text-xs text-muted-foreground">{member.email}</p>
          </div>
        </div>
      </TableCell>
      <TableCell>{ROLE_LABELS[member.role] ?? member.role}</TableCell>
      <MemberStatusCells member={member} />
    </TableRow>
  )
}

/** Status badge and join-date cells shared by both member tables. */
function MemberStatusCells({ member }: { member: MemberWithUser }) {
  return (
    <>
      <TableCell>
        <Badge variant={member.status === 'active' ? 'secondary' : 'outline'}>
          {STATUS_LABELS[member.status] ?? member.status}
        </Badge>
      </TableCell>
      <TableCell className="text-muted-foreground">
        {format(new Date(member.joinedAt), 'yyyy年M月d日')}
      </TableCell>
    </>
  )
}

/**
 * Dialog for adding an already-registered user to the space by email. Only
 * admin/owner actors see the trigger; the new member is always created with the
 * fixed `member` role (role management happens on the row). The dialog closes on
 * success and the membership list refreshes via query invalidation. An unknown
 * email surfaces the backend `user_not_registered` fault as a friendly hint.
 */
function AddMemberDialog({
  open,
  onOpenChange,
  tenantId,
  spaceId,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  tenantId: string
  spaceId: string
}) {
  const [email, setEmail] = useState('')
  const [attempted, setAttempted] = useState(false)
  const addMember = useAddSpaceMemberByEmail(tenantId, spaceId)
  const errorCode = addMember.error?.response?.data?.code

  // Local validation only: empty or malformed addresses stop before the request.
  // `attempted` gates the hints so a pristine field stays quiet until first submit.
  let localHint: string | undefined
  if (attempted && email.trim() === '') {
    localHint = '请输入邮箱地址。'
  } else if (attempted && !email.includes('@')) {
    localHint = '请输入有效的邮箱地址。'
  }
  let serverHint: string | undefined
  if (errorCode === 'user_not_registered') {
    serverHint = '该邮箱尚未注册，请先完成注册。'
  } else if (errorCode === 'space_role_required') {
    serverHint = '你没有权限添加成员。'
  } else if (errorCode) {
    serverHint = `添加失败：${errorCode}`
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>添加成员</DialogTitle>
          <DialogDescription>输入已注册用户的邮箱，将其添加为普通成员。</DialogDescription>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (!attempted) setAttempted(true)
            if (email.trim() === '' || !email.includes('@') || addMember.isPending) return
            addMember.mutate(
              { email: email.trim() },
              {
                onSuccess: () => {
                  onOpenChange(false)
                  setEmail('')
                  setAttempted(false)
                },
              },
            )
          }}
          noValidate
          className="space-y-4"
        >
          <DialogFormField
            id="new-member-email"
            label="成员邮箱"
            value={email}
            onChange={setEmail}
            placeholder="member@example.com"
            hint={localHint ?? serverHint}
            required
          />
          <Button type="submit" className="w-full" disabled={addMember.isPending}>
            {addMember.isPending ? '添加中…' : '添加'}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Cloud membership table with management controls. Adding members is open to
 * admins and owners; changing roles and disabling members is admin or owner;
 * removing a member is owner-only. Actors without admin or owner see a
 * read-only table; owners alone can grant owner. The last owner cannot be
 * demoted, disabled or removed (the backend enforces `space_last_owner`).
 */
function CloudMembersView({
  tenantId,
  spaceId,
  members,
  isPending,
  myRole,
}: {
  tenantId: string
  spaceId: string
  members: MemberWithUser[]
  isPending: boolean
  myRole: SpaceRole
}) {
  const [addOpen, setAddOpen] = useState(false)
  const updateMember = useUpdateSpaceMember(tenantId, spaceId)
  const removeMember = useRemoveSpaceMember(tenantId, spaceId)
  const canManage = myRole === 'admin' || myRole === 'owner'
  const canGrantOwner = myRole === 'owner'
  const errorCode =
    updateMember.error?.response?.data?.code ?? removeMember.error?.response?.data?.code

  function update(role: SpaceRole, status: 'active' | 'disabled', member: MemberWithUser) {
    updateMember.mutate({ userId: member.id, role, status, version: member.version ?? 0 })
  }

  return (
    <div className="p-4">
      {canManage && (
        <div className="mb-4">
          <Button onClick={() => setAddOpen(true)}>添加成员</Button>
          <AddMemberDialog
            open={addOpen}
            onOpenChange={setAddOpen}
            tenantId={tenantId}
            spaceId={spaceId}
          />
        </div>
      )}
      {errorCode && <p className="mb-3 text-xs text-destructive">操作失败：{errorCode}</p>}
      {isPending && (
        <div className="space-y-2">
          {SKELETON_KEYS.map((key) => (
            <Skeleton key={key} className="h-10 w-full" />
          ))}
        </div>
      )}
      {members.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>成员</TableHead>
              <TableHead>角色</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>加入时间</TableHead>
              {canManage && <TableHead>操作</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {members.map((member) => (
              <CloudMemberRow
                key={member.id}
                member={member}
                canManage={canManage}
                canGrantOwner={canGrantOwner}
                pending={updateMember.isPending || removeMember.isPending}
                onUpdate={update}
                onRemove={(userId, version) => removeMember.mutate({ userId, version })}
              />
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}

/**
 * One cloud membership row with role selector, disable/enable and (for owners)
 * remove controls. The backend rejects granting owner without an owner actor
 * and demoting/disabling/removing the last owner; errors surface above the
 * table. Owner rows cannot be disabled or removed.
 */
function CloudMemberRow({
  member,
  canManage,
  canGrantOwner,
  pending,
  onUpdate,
  onRemove,
}: {
  member: MemberWithUser
  canManage: boolean
  canGrantOwner: boolean
  pending: boolean
  onUpdate: (role: SpaceRole, status: 'active' | 'disabled', member: MemberWithUser) => void
  onRemove: (userId: string, version: number) => void
}) {
  const role = normalizeSpaceRole(member.role)
  const isOwnerRow = role === 'owner'
  return (
    <TableRow>
      <TableCell>
        <div className="flex items-center gap-2">
          <ActorAvatar actor={member} size="sm" />
          <div>
            <p className="text-sm font-medium">{member.name}</p>
            <p className="text-xs text-muted-foreground">{member.id}</p>
          </div>
        </div>
      </TableCell>
      <TableCell>
        {canManage ? (
          <Select
            value={role}
            onValueChange={(value) =>
              onUpdate(normalizeSpaceRole(value ?? 'member'), 'active', member)
            }
          >
            <SelectTrigger className="w-28" aria-label={`${member.name} 的角色`}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {roleOptions(canGrantOwner).map((option) => (
                <SelectItem key={option} value={option}>
                  {ROLE_LABELS[option]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          (ROLE_LABELS[role] ?? role)
        )}
      </TableCell>
      <MemberStatusCells member={member} />
      {canManage && (
        <TableCell>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={pending || role === 'owner'}
              onClick={() =>
                onUpdate(role, member.status === 'active' ? 'disabled' : 'active', member)
              }
            >
              {member.status === 'active' ? '禁用' : '启用'}
            </Button>
            {canGrantOwner && !isOwnerRow && (
              <RemoveMemberDialog
                member={member}
                pending={pending}
                onRemove={() => onRemove(member.id, member.version ?? 0)}
              />
            )}
          </div>
        </TableCell>
      )}
    </TableRow>
  )
}

/**
 * Owner-only removal of a member's workspace membership, confirmed in a dialog.
 * Removal is a hard delete of the workspace membership alone — the user
 * account, their tenant membership and any resources they created are
 * untouched and remain in the workspace. Owner rows are never offered for
 * removal (demote them first; the backend rejects last-owner removal with 409
 * space_last_owner, surfaced above the table).
 */
function RemoveMemberDialog({
  member,
  pending,
  onRemove,
}: {
  member: MemberWithUser
  pending: boolean
  onRemove: () => void
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={
          <Button variant="destructive" size="sm">
            移除
          </Button>
        }
      />
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>从工作区移除「{member.name}」？</AlertDialogTitle>
          <AlertDialogDescription>
            该成员将无法再访问此工作区及其项目。其创建的资源会保留在工作区中，账号与租户成员关系不受影响。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction disabled={pending} onClick={onRemove}>
            确认移除
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
