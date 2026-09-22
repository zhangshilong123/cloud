import { useCallback, useEffect, useRef, useState } from 'react'
import { Navigate, useSearchParams } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  classifyAuthenticationFailure,
  useCurrentUser,
  useDemoLogin,
  useStartLogin,
} from '@/features/auth/api'
import { safeReturnTo } from '@/features/auth/return-to'
import { db } from '@/mocks/data/store'
import { useDemoAuthStore } from '@/state/demo-auth-store'

function replaceBrowserLocation(target: string) {
  window.location.replace(target)
}

/** Dependencies for the automatic external navigation owned by the login screen. */
export interface LoginPageProps {
  replaceLocation?: (target: string) => void
}

/**
 * Sign-in offers two planes. The real-account plane verifies the Gateway
 * HttpOnly cookie through `/api/v1/me` and starts the external IDaaS login
 * when absent. The demo plane signs in against the MSW-mocked store
 * (`/mock-api/auth/login`, any email) and drives the local mock pages without
 * touching the real backend.
 */
export function LoginPage({ replaceLocation = replaceBrowserLocation }: LoginPageProps) {
  const [search] = useSearchParams()
  const returnTo = safeReturnTo(search.get('returnTo'))
  const [tab, setTab] = useState<'cloud' | 'demo'>('cloud')
  const demoToken = useDemoAuthStore((s) => s.token)
  const loggedOut = useDemoAuthStore((s) => s.loggedOut)
  const currentUser = useCurrentUser()
  const login = useStartLogin()
  const attempted = useRef(false)
  const failure = currentUser.isError ? classifyAuthenticationFailure(currentUser.error) : undefined

  const beginLogin = useCallback(() => {
    attempted.current = true
    login.mutate(returnTo, {
      onSuccess: ({ authorizationUrl }) => replaceLocation(authorizationUrl),
    })
  }, [login, replaceLocation, returnTo])

  // Only the real-account tab drives the IDaaS redirect; the demo tab never
  // calls /auth/*.
  useEffect(() => {
    if (tab === 'cloud' && failure === 'unauthenticated' && !attempted.current) beginLogin()
  }, [tab, beginLogin, failure])

  if (demoToken) return <Navigate to={`/${db.workspace.slug}/issues`} replace />
  // After an explicit sign-out the session is not trusted anymore, even when
  // the bridge keeps answering /api/v1/me (demo bridge has no /auth/logout);
  // stay on the sign-in screen instead of bouncing straight back in.
  if (currentUser.isSuccess && !loggedOut) return <Navigate to={returnTo} replace />

  return (
    <div className="flex min-h-svh items-center justify-center bg-muted/30 px-4">
      <div className="w-full max-w-sm space-y-6">
        <div className="space-y-1 text-center">
          <div className="mx-auto flex size-10 items-center justify-center rounded-lg bg-primary text-primary-foreground font-semibold">
            O
          </div>
          <h1 className="text-lg font-semibold">登录 Ora</h1>
          <p className="text-sm text-muted-foreground">
            真实账号连接后端协作空间；演示账号使用本地模拟数据。
          </p>
        </div>
        <Tabs value={tab} onValueChange={(v) => setTab(v === 'demo' ? 'demo' : 'cloud')}>
          <TabsList className="grid w-full grid-cols-2">
            <TabsTrigger value="cloud">真实账号</TabsTrigger>
            <TabsTrigger value="demo">演示账号</TabsTrigger>
          </TabsList>
          <TabsContent value="cloud">
            <CloudAuthPanel
              loggedOut={loggedOut}
              failure={failure}
              login={login}
              onRetryCurrentUser={() => void currentUser.refetch()}
              onRetryLogin={() => {
                attempted.current = false
                beginLogin()
              }}
            />
          </TabsContent>
          <TabsContent value="demo">
            <DemoSignInForm />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}

/**
 * Real-account authentication status: starts the external login on
 * unauthenticated, surfaces disabled/unavailable states, and offers retries.
 */
function CloudAuthPanel({
  loggedOut,
  failure,
  login,
  onRetryCurrentUser,
  onRetryLogin,
}: {
  loggedOut: boolean
  failure: ReturnType<typeof classifyAuthenticationFailure> | undefined
  login: ReturnType<typeof useStartLogin>
  onRetryCurrentUser: () => void
  onRetryLogin: () => void
}) {
  let title = '正在验证登录状态…'
  let description = '请稍候。'
  let action: React.ReactNode

  if (loggedOut) {
    // The demo bridge has no /auth/logout route, so /api/v1/me still answers a
    // user after sign-out; surface the sign-out instead of re-authenticating.
    title = '已退出登录'
    description = '如需重新登录，请切换至「演示账号」或刷新页面。'
  } else if (failure === 'forbidden') {
    title = '账号已被停用'
    description = 'Cloud 已拒绝当前账号，请联系管理员恢复访问。'
  } else if (failure === 'unavailable') {
    title = '暂时无法验证登录状态'
    description = 'Cloud 或认证网关暂时不可用。'
    action = <Button onClick={onRetryCurrentUser}>重新检查</Button>
  } else if (login.isError) {
    title = '无法连接华为统一登录'
    description = '登录尚未开始，可以安全重试。'
    action = (
      <Button
        onClick={() => {
          login.reset()
          onRetryLogin()
        }}
      >
        重新登录
      </Button>
    )
  } else if (failure === 'unauthenticated') {
    title = '正在跳转华为统一登录…'
    description = '登录成功后将自动返回 Cloud。'
  }

  return (
    <div className="space-y-4 text-center">
      <div className="space-y-1">
        <h2 className="text-base font-medium">{title}</h2>
        <p className="text-sm text-muted-foreground">{description}</p>
      </div>
      {action}
    </div>
  )
}

/** Demo sign-in against the mock store; any email works. */
function DemoSignInForm() {
  const [email, setEmail] = useState('')
  const login = useDemoLogin()

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        const value = email.trim()
        if (!value) return
        // The demo session lands on the mock workspace; LoginPage redirects
        // there as soon as the demo store holds a token.
        login.mutate(value)
      }}
      className="space-y-4"
    >
      <div className="space-y-1.5">
        <Label htmlFor="demo-email">邮箱</Label>
        <Input
          id="demo-email"
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="you@example.com"
          autoComplete="email"
          required
        />
      </div>
      <Button type="submit" className="w-full" disabled={login.isPending}>
        {login.isPending ? '登录中…' : '进入演示'}
      </Button>
      <p className="text-center text-xs text-muted-foreground">
        演示模式使用本地模拟数据，无需后端即可体验智能体、小队、技能、运行时等页面。
      </p>
    </form>
  )
}
