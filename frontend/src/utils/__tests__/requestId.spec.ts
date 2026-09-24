import { afterEach, describe, expect, it, vi } from 'vitest'
import { createRequestId } from '../requestId'

afterEach(() => vi.unstubAllGlobals())

describe('createRequestId', () => {
  it('uses the native UUID API when available', () => {
    const randomUUID = vi.fn(() => 'f0e748a3-c5ae-4fa2-b898-236795d8c6de')
    vi.stubGlobal('crypto', { randomUUID })
    expect(createRequestId()).toBe('f0e748a3-c5ae-4fa2-b898-236795d8c6de')
    expect(randomUUID).toHaveBeenCalledOnce()
  })

  it('creates RFC 4122 v4 IDs using getRandomValues on HTTP origins', () => {
    let seed = 0
    const getRandomValues = vi.fn((bytes: Uint8Array) => {
      bytes.forEach((_, index) => { bytes[index] = seed++ % 256 })
      return bytes
    })
    vi.stubGlobal('crypto', { getRandomValues })
    const first = createRequestId()
    expect(first).toBe('00010203-0405-4607-8809-0a0b0c0d0e0f')
    expect(createRequestId()).not.toBe(first)
    expect(getRandomValues).toHaveBeenCalledTimes(2)
  })

  it('fails explicitly instead of falling back to weak randomness', () => {
    vi.stubGlobal('crypto', undefined)
    expect(createRequestId).toThrow('Secure random values are unavailable')
  })
})
