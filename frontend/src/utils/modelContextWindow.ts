export const DEFAULT_CONTEXT_WINDOW_TOKENS = 8192
export const MIN_CONTEXT_WINDOW_TOKENS = 4096
export const MAX_CONTEXT_WINDOW_TOKENS = 2_000_000

export type ContextWindowModelType = 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'

export function isValidContextWindowTokens(value: unknown): boolean {
  if (value == null || value === '') return true
  const numericValue = Number(value)
  if (!Number.isInteger(numericValue)) return false
  return numericValue === 0
    || (numericValue >= MIN_CONTEXT_WINDOW_TOKENS && numericValue <= MAX_CONTEXT_WINDOW_TOKENS)
}

export function contextWindowParameters(
  modelType: ContextWindowModelType,
  value: unknown,
): { context_window_tokens?: number } {
  if (modelType !== 'chat' && modelType !== 'vllm') return {}
  const numericValue = Number(value)
  if (!Number.isInteger(numericValue) || numericValue <= 0) return {}
  return { context_window_tokens: numericValue }
}
