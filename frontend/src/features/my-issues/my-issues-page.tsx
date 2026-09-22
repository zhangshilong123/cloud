import { useCurrentUser } from '@/features/auth/api'
import { IssuesList } from '@/features/issues/issues-list'

export function MyIssuesPage({ slug }: { slug: string }) {
  const currentUser = useCurrentUser()
  const userId = currentUser.data?.id
  return <IssuesList slug={slug} title="我的任务" {...(userId ? { assigneeUserId: userId } : {})} />
}
