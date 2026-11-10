import { describe, expect, it } from 'vitest'
import { listRoute, parseRoute, sessionRoute } from './route'

describe('hash routes', () => {
  it('round-trips encoded session identities', () => {
    const hash = sessionRoute('a/b', 'id /% 漢字')
    expect(hash).toBe('#/sessions/a%2Fb/id%20%2F%25%20%E6%BC%A2%E5%AD%97')
    expect(parseRoute(hash)).toEqual({ kind: 'session', harness: 'a/b', nativeSessionId: 'id /% 漢字' })
  })
  it.each(['', '#', '#/', '#/unknown', '#/sessions/a/b/', '#/sessions//b', '#/sessions/a/b/c', '#/sessions/a/%xx'])('falls back to list for %s', (hash) => {
    expect(parseRoute(hash)).toEqual({ kind: 'list' })
  })
  it('serializes the list route', () => expect(listRoute()).toBe('#/'))
})
