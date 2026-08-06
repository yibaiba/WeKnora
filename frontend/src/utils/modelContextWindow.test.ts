import assert from 'node:assert/strict'
import test from 'node:test'

import {
  DEFAULT_CONTEXT_WINDOW_TOKENS,
  MAX_CONTEXT_WINDOW_TOKENS,
  MIN_CONTEXT_WINDOW_TOKENS,
  contextWindowParameters,
  contextWindowTokensFromParameters,
  isValidContextWindowTokens,
  suggestedContextWindowTokens,
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
  assert.throws(() => contextWindowParameters('chat', 4095), /Invalid context_window_tokens/)
})

test('round-trips configured context windows through API parameters', () => {
  const parameters = contextWindowParameters('chat', 32768)
  assert.equal(contextWindowTokensFromParameters(parameters), 32768)
  assert.equal(contextWindowTokensFromParameters(undefined), 0)
  assert.throws(
    () => contextWindowTokensFromParameters({ context_window_tokens: 2_000_001 }),
    /Invalid context_window_tokens/,
  )
})

test('suggests known remote model context windows by model name', () => {
  const cases = [
    ['deepseek-chat', 65_536],
    ['deepseek-v4-pro', 1_000_000],
    ['openai/gpt-4.1-mini', 1_047_576],
    ['gpt-5.4', 1_050_000],
    ['gpt-5.6-sol', 258_400],
    ['gpt-5.6-terra', 258_400],
    ['gpt-5.6-luna', 258_400],
    ['gpt-5.2', 400_000],
    ['anthropic/claude-fable-5', 1_000_000],
    ['claude-opus-5', 1_000_000],
    ['claude-sonnet-5', 1_000_000],
    ['anthropic.claude-haiku-4-5-20251001-v1:0', 200_000],
    ['gemini-3.6-flash', 1_048_576],
    ['gemini-3.5-flash-lite', 1_048_576],
    ['gemini-2.5-pro', 1_048_576],
    ['qwen3.7-plus', 1_000_000],
    ['qwen3.7-flash', 1_000_000],
    ['qwen3-max', 262_144],
    ['kimi/kimi-k3', 1_000_000],
    ['glm-5.2', 1_000_000],
    ['glm-4.5-air', 131_072],
    ['meta-llama/llama-3.3-70b-instruct', 131_072],
  ] as const

  for (const [modelName, expected] of cases) {
    assert.equal(suggestedContextWindowTokens({ modelName, source: 'remote' }), expected, modelName)
  }
})

test('suggests current Alibaba Cloud model context windows', () => {
  const cases = [
    ['qwen3.8-max-preview', 983_616],
    ['qwen3.7-max-2026-06-08', 1_000_000],
    ['kimi/kimi-k3', 1_000_000],
    ['kimi-k2.7-code', 262_144],
    ['MiniMax/MiniMax-M3', 196_608],
    ['mimo-v2.5-pro', 1_000_000],
  ] as const

  for (const [modelName, expected] of cases) {
    assert.equal(suggestedContextWindowTokens({ provider: 'aliyun', modelName }), expected, modelName)
  }
})

test('uses Ollama-specific context windows for local and Ollama Cloud models', () => {
  assert.equal(suggestedContextWindowTokens({
    provider: 'ollama_cloud', modelName: 'kimi-k3:cloud', source: 'remote',
  }), 1_000_000)
  assert.equal(suggestedContextWindowTokens({
    provider: 'ollama_cloud', modelName: 'glm-5.1:cloud', source: 'remote',
  }), 202_752)
  assert.equal(suggestedContextWindowTokens({
    provider: 'ollama_cloud', modelName: 'deepseek-v3.1:671b-cloud', source: 'remote',
  }), 163_840)
  assert.equal(suggestedContextWindowTokens({
    provider: 'ollama_cloud', modelName: 'gpt-oss:120b-cloud', source: 'remote',
  }), 131_072)
  assert.equal(suggestedContextWindowTokens({ modelName: 'deepseek-v3:671b-q8_0', source: 'local' }), 4_096)
  assert.equal(suggestedContextWindowTokens({ modelName: 'qwen3:8b', source: 'local' }), 40_960)
  assert.equal(suggestedContextWindowTokens({ modelName: 'qwen3:30b', source: 'local' }), 262_144)
  assert.equal(suggestedContextWindowTokens({ modelName: 'qwen3.5:9b', source: 'local' }), 262_144)
  assert.equal(suggestedContextWindowTokens({ modelName: 'gemma3:1b', source: 'local' }), 32_768)
})

test('does not guess custom or ambiguous model names', () => {
  assert.equal(suggestedContextWindowTokens({ modelName: 'company-gpt-4o-proxy' }), undefined)
  assert.equal(suggestedContextWindowTokens({ modelName: 'qwen3-custom' }), undefined)
  assert.equal(suggestedContextWindowTokens({ modelName: 'qwen3.8-max-preview' }), undefined)
  assert.equal(suggestedContextWindowTokens({ modelName: '' }), undefined)
})
