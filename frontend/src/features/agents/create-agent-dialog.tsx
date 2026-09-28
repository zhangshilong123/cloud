import { useState } from 'react'
import { DialogFormField } from '@/components/common/dialog-form-field'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useCreateAgent } from '@/features/agents/api'

/**
 * Dialog for creating an Agent in the current space. The cloud Agent model is
 * minimal (a name); status and bindings are managed on the detail page. The
 * dialog closes on success and the list refetches via query invalidation.
 */
export function CreateAgentDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const createAgent = useCreateAgent()
  const [name, setName] = useState('')

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>新建智能体</DialogTitle>
          <DialogDescription>
            创建一个 Agent，之后为其绑定 Skill 并演示 Execution。
          </DialogDescription>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (!name.trim() || createAgent.isPending) return
            createAgent.mutate(
              { name: name.trim() },
              {
                onSuccess: () => {
                  setName('')
                  onOpenChange(false)
                },
              },
            )
          }}
          noValidate
          className="space-y-4"
        >
          <DialogFormField
            id="new-agent-name"
            label="名称"
            value={name}
            onChange={setName}
            placeholder="Demo Agent"
            required
          />
          {createAgent.error && (
            <p className="text-xs text-destructive">
              创建失败：{createAgent.error.response?.data?.code}
            </p>
          )}
          <Button type="submit" className="w-full" disabled={!name.trim() || createAgent.isPending}>
            {createAgent.isPending ? '创建中…' : '创建'}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}
