import { Navigate, Outlet, useLocation, useNavigate, useParams } from 'react-router-dom'
import type { User } from '@/api/generated.schemas'
import { AppSidebar } from '@/components/layout/app-sidebar'
import { Button } from '@/components/ui/button'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { classifyAuthenticationFailure, useCurrentUser, useLogout } from '@/features/auth/api'
import { loginPath } from '@/features/auth/return-to'
import { CurrentSpaceProvider, useCurrentSpace } from '@/features/spaces/current-space'
import { useSpaceEvents } from '@/features/spaces/use-space-events'
import { useDemoAuthStore } from '@/state/demo-auth-store'

/**
 * Authenticated application shell. A demo session (MSW mock store) renders
 * without touching `/api/v1/me`; a cloud session is verified against the
 * Gateway through `/api/v1/me` before any workspace content is rendered.
 */
export function DashboardLayout() {
  const { workspaceSlug } = useParams<{ workspaceSlug: string }>()
  const demoToken = useDemoAuthStore((s) => s.token)
  if (demoToken) return <DemoDashboard slug={workspaceSlug ?? ''} />
  return <CloudDashboard slug={workspaceSlug ?? ''} />
}

/**
 * Demo-plane shell: resolves the slug against the mock store, never calls the
 * real backend, and keeps `cloudMode` false so the mock-only surfaces (AI 团队
 * and friends) are reachable.
 */
function DemoDashboard({ slug }: { slug: string }) {
  return (
    <CurrentSpaceProvider slug={slug}>
      <DashboardShell slug={slug} user={undefined} />
    </CurrentSpaceProvider>
  )
}

/** Cloud-plane shell: Gateway cookie verified through `/api/v1/me`. */
function CloudDashboard({ slug }: { slug: string }) {
  const location = useLocation()
  const currentUser = useCurrentUser()

  if (currentUser.isPending) {
    return <div className="flex min-h-svh items-center justify-center">正在验证登录状态…</div>
  }
  if (currentUser.isError) {
    const failure = classifyAuthenticationFailure(currentUser.error)
    if (failure === 'unauthenticated') {
      return (
        <Navigate to={loginPath(location.pathname + location.search + location.hash)} replace />
      )
    }
    if (failure === 'forbidden') {
      return <div className="flex min-h-svh items-center justify-center">账号已被停用</div>
    }
    return (
      <div className="flex min-h-svh flex-col items-center justify-center gap-4">
        <p>暂时无法验证登录状态</p>
        <Button onClick={() => void currentUser.refetch()}>重新检查</Button>
      </div>
    )
  }

  return (
    <CurrentSpaceProvider slug={slug} authenticated>
      <DashboardShell slug={slug} user={currentUser.data} />
    </CurrentSpaceProvider>
  )
}

function DashboardShell({ slug, user }: { slug: string; user: User | undefined }) {
  const { tenantId, space, spaces } = useCurrentSpace()
  useSpaceEvents(tenantId, space?.id)
  if (spaces && spaces.length === 0) {
    return <EmptyWorkspaceState />
  }
  if (space === undefined && spaces && spaces.length > 0) {
    const first = spaces[0]
    if (first) return <Navigate to={`/${first.slug}/projects`} replace />
  }
  return (
    <SidebarProvider className="h-svh">
      <AppSidebar slug={space?.slug ?? slug} user={user} />
      <SidebarInset className="min-h-0 overflow-hidden">
        <Outlet />
      </SidebarInset>
    </SidebarProvider>
  )
}

/** An authenticated user with no joined space: nothing to leak or mock. */
function EmptyWorkspaceState() {
  const navigate = useNavigate()
  const logout = useLogout()

  function handleLogout() {
    logout.mutate(undefined, { onSuccess: () => void navigate('/login') })
  }

  return (
    <div className="flex h-svh items-center justify-center bg-muted/30">
      <div className="space-y-3 text-center">
        <p className="text-sm text-muted-foreground">你尚未加入任何工作区，请联系管理员添加</p>
        <Button variant="outline" disabled={logout.isPending} onClick={handleLogout}>
          {logout.isPending ? '正在退出…' : '退出登录'}
        </Button>
      </div>
    </div>
  )
}
