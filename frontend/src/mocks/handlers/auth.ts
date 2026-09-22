import { http, HttpResponse } from 'msw'
import { currentUserId, db } from '@/mocks/data/store'
import { MOCK_BASE, jsonObject, stringField } from './shared'

const MOCK_TOKEN = 'mock-session-token'

/**
 * Demo-plane auth for the MSW-mocked store. Any email signs the seeded user
 * in; the session lives only in the demo store and never reaches /auth/* or
 * /api/v1/me.
 */
export const authHandlers = [
  http.post(`${MOCK_BASE}/auth/login`, async ({ request }) => {
    const body = jsonObject(await request.json())
    const user = db.users.find((u) => u.id === currentUserId)
    if (!user) return HttpResponse.json({ message: 'user not found' }, { status: 404 })
    return HttpResponse.json({
      token: MOCK_TOKEN,
      user: { ...user, email: stringField(body['email']) || user.email },
    })
  }),

  http.get(`${MOCK_BASE}/auth/session`, ({ request }) => {
    const auth = request.headers.get('authorization')
    if (auth !== `Bearer ${MOCK_TOKEN}`) {
      return HttpResponse.json({ message: 'unauthenticated' }, { status: 401 })
    }
    const user = db.users.find((u) => u.id === currentUserId)
    if (!user) return HttpResponse.json({ message: 'user not found' }, { status: 404 })
    return HttpResponse.json({ user })
  }),

  http.post(`${MOCK_BASE}/auth/logout`, () => new HttpResponse(null, { status: 204 })),
]
