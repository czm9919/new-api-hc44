import { describe, expect, it } from 'vitest'

import { maskedBaseUrl } from '../ingress-card'

describe('maskedBaseUrl', () => {
  it('masks the 8-hex prefix at the end (root form)', () => {
    expect(maskedBaseUrl('https://accel.example.com/a3f2b91c')).toBe(
      'https://accel.example.com/a3f2••••'
    )
  })

  it('masks the prefix even when /v1 follows it (OpenAI form)', () => {
    expect(maskedBaseUrl('https://accel.example.com/a3f2b91c/v1')).toBe(
      'https://accel.example.com/a3f2••••/v1'
    )
  })

  it('leaves host-only urls untouched', () => {
    expect(maskedBaseUrl('https://accel.example.com/v1')).toBe(
      'https://accel.example.com/v1'
    )
  })

  it('does not mask non-hex segments of the same length', () => {
    expect(maskedBaseUrl('https://accel.example.com/dashboard')).toBe(
      'https://accel.example.com/dashboard'
    )
  })
})
