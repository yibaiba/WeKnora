import assert from 'node:assert/strict'
import test from 'node:test'

import {
  DEFAULT_CONTEXT_WINDOW_TOKENS,
  MAX_CONTEXT_WINDOW_TOKENS,
  MIN_CONTEXT_WINDOW_TOKENS,
  contextWindowParameters,
  isValidContextWindowTokens,
} from './modelContextWindow.ts'

test('validates default and configured context window boundaries', () => {
  for (const value of [undefined, '', 0, MIN_CONTEXT_WINDOW_TOKENS, MAX_CONTEXT_WINDOW_TOKENS]) {
    assert.equal(isValidContextWindowTokens(value), true, String(value))
  }
  for (const value of [-1, MIN_CONTEXT_WINDOW_TOKENS - 1, MAX_CONTEXT_WINDOW_TOKENS + 1, 8192.5]) {
    assert.equal(isValidContextWindowTokens(value), false, String(value))
  }
  assert.equal(DEFAULT_CONTEXT_WINDOW_TOKENS, 8192)
})

test('serializes context window only for chat-style model types and configured values', () => {
  assert.deepEqual(contextWindowParameters('chat', 32768), { context_window_tokens: 32768 })
  assert.deepEqual(contextWindowParameters('vllm', 65536), { context_window_tokens: 65536 })
  assert.deepEqual(contextWindowParameters('embedding', 32768), {})
  assert.deepEqual(contextWindowParameters('chat', 0), {})
})
