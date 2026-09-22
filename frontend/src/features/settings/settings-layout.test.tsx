import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { SidebarProvider } from '@/components/ui/sidebar'
import { CurrentSpaceProvider } from '@/features/spaces/current-space'
import { GeneralSettingsPage } from '@/features/settings/general-settings-page'
import { CurrentSpaceProvider } from '@/features/spaces/current-space'
import { db } from '@/mocks/data/store'
import { SettingsLayout } from './settings-layout'

function renderSettings() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createMemoryRouter(
    [
      {
        path: '/:workspaceSlug/settings',
        element: <SettingsLayout slug={db.workspace.slug} />,
        children: [
          { index: true, element: <GeneralSettingsPage /> },
          { path: 'members', element: <div>Members screen</div> },
        ],
      },
    ],
    { initialEntries: [`/${db.workspace.slug}/settings`] },
  )
  return render(
    <QueryClientProvider client={queryClient}>
      <SidebarProvider>
        <CurrentSpaceProvider slug={db.workspace.slug}>
          <RouterProvider router={router} />
        </CurrentSpaceProvider>
      </SidebarProvider>
    </QueryClientProvider>,
  )
}

describe('SettingsLayout', () => {
  it('shows General by default and navigates to Members on tab click', async () => {
    const user = userEvent.setup()
    renderSettings()

    expect(await screen.findByText(db.workspace.name)).toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: '成员' }))
    expect(await screen.findByText('Members screen')).toBeInTheDocument()
  })
})
