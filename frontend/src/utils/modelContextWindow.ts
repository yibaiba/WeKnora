export const DEFAULT_CONTEXT_WINDOW_TOKENS = 8192
export const MIN_CONTEXT_WINDOW_TOKENS = 4096
export const MAX_CONTEXT_WINDOW_TOKENS = 2_000_000

export type ContextWindowModelType = 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'

export interface ContextWindowModelIdentity {
  modelName?: string
  provider?: string
  source?: 'local' | 'remote'
}

interface ContextWindowRule {
  pattern: RegExp
  tokens: number
}

// Verified against the providers' model catalogs on 2026-08-06. Provider-specific
// rules run first because hosted variants can expose different context limits.
const OLLAMA_CONTEXT_WINDOW_RULES: readonly ContextWindowRule[] = [
  { pattern: /^deepseek-v4-(?:pro|flash)(?::|$)/, tokens: 1_000_000 },
  { pattern: /^kimi-k3(?::|$)/, tokens: 1_000_000 },
  { pattern: /^glm-5\.1(?::|$)/, tokens: 202_752 },
  { pattern: /^qwen3\.5(?::|$)/, tokens: 262_144 },
  { pattern: /^deepseek-v3:671b-(?:q8_0|fp16)$/, tokens: 4_096 },
  { pattern: /^deepseek-v3(?:\.1)?(?::|$)/, tokens: 163_840 },
  { pattern: /^deepseek-r1:671b(?:-|$)/, tokens: 163_840 },
  { pattern: /^deepseek-r1(?::|$)/, tokens: 131_072 },
  { pattern: /^gpt-oss(?::|$)/, tokens: 131_072 },
  { pattern: /^qwen3-coder(?::|$)/, tokens: 262_144 },
  { pattern: /^qwen3:(?:4b-(?:instruct|thinking)|30b|235b)(?:-|$)/, tokens: 262_144 },
  { pattern: /^qwen3(?::|$)/, tokens: 40_960 },
  { pattern: /^llama3\.[123](?::|$)/, tokens: 131_072 },
  { pattern: /^llama3(?::|$)/, tokens: 8_192 },
  { pattern: /^gemma3:(?:270m|1b)(?:-|$)/, tokens: 32_768 },
  { pattern: /^gemma3(?::|$)/, tokens: 131_072 },
]

const ALIYUN_CONTEXT_WINDOW_RULES: readonly ContextWindowRule[] = [
  { pattern: /^qwen3\.8-max-preview(?:[-:]|$)/, tokens: 983_616 },
  { pattern: /^kimi-k3(?:[-:]|$)/, tokens: 1_000_000 },
  { pattern: /^kimi-k2\.(?:6|7)(?:[-:]|$)/, tokens: 262_144 },
  { pattern: /^minimax-m3(?:[-:]|$)/, tokens: 196_608 },
  { pattern: /^mimo-v2\.5-pro(?:[-:]|$)/, tokens: 1_000_000 },
]

const COMMON_CONTEXT_WINDOW_RULES: readonly ContextWindowRule[] = [
  { pattern: /^deepseek-v4-(?:pro|flash)(?:[-:]|$)/, tokens: 1_000_000 },
  { pattern: /^deepseek-(?:chat|reasoner)(?:[-:]|$)/, tokens: 65_536 },
  { pattern: /^deepseek-v3(?:\.[12])?(?:[-:]|$)/, tokens: 131_072 },
  { pattern: /^deepseek-r1(?:[-:]|$)/, tokens: 131_072 },
  // Codex exposes 258,400 usable tokens for GPT-5.6, below the API model-card maximum.
  { pattern: /^gpt-5\.6(?:-(?:sol|terra|luna))?(?:-\d{4}-\d{2}-\d{2})?$/, tokens: 258_400 },
  { pattern: /^gpt-5\.4(?:-pro)?(?:-\d{4}-\d{2}-\d{2})?$/, tokens: 1_050_000 },
  { pattern: /^gpt-5(?:\.(?:1|2))?(?:[-:]|$)/, tokens: 400_000 },
  { pattern: /^gpt-4\.1(?:[-:]|$)/, tokens: 1_047_576 },
  { pattern: /^gpt-4o(?:[-:]|$)/, tokens: 128_000 },
  { pattern: /^o(?:3|4)(?:[-:]|$)/, tokens: 200_000 },
  { pattern: /^gpt-oss(?:[-:]|$)/, tokens: 131_072 },
  { pattern: /^claude-(?:fable-5|mythos-(?:5|preview)|opus-(?:5|4-[678])|sonnet-(?:5|4-6))(?:[-:@]|$)/, tokens: 1_000_000 },
  { pattern: /^claude-(?:haiku-4-5|sonnet-4-5)(?:[-:@]|$)/, tokens: 200_000 },
  { pattern: /^gemini-1\.5-pro(?:[-:]|$)/, tokens: 2_000_000 },
  { pattern: /^gemini-(?:1\.5|[23](?:\.\d+)?)(?:[-:]|$)/, tokens: 1_048_576 },
  { pattern: /^qwen3\.6-max(?:-preview)?(?:[-:]|$)/, tokens: 262_144 },
  { pattern: /^qwen3\.(?:7-(?:max|plus|flash)|6-(?:plus|flash)|5-(?:plus|flash))(?:[-:]|$)/, tokens: 1_000_000 },
  { pattern: /^qwen-(?:plus|flash)(?::|$)/, tokens: 1_000_000 },
  { pattern: /^qwen3-coder-plus(?:[-:]|$)/, tokens: 1_000_000 },
  { pattern: /^qwen3-(?:max|coder-next)(?:[-:]|$)/, tokens: 262_144 },
  { pattern: /^kimi-k3(?:[-:]|$)/, tokens: 1_000_000 },
  { pattern: /^glm-5\.2(?:[-:]|$)/, tokens: 1_000_000 },
  { pattern: /^glm-(?:5(?:-turbo)?|4\.7)(?:[-:]|$)/, tokens: 204_800 },
  { pattern: /^glm-4\.5(?:[-:]|$)/, tokens: 131_072 },
  { pattern: /^llama-?3\.[123](?:[-:]|$)/, tokens: 131_072 },
]

function normalizedModelName(modelName: string): string {
  const unqualifiedName = modelName.trim().toLowerCase().split('/').at(-1) ?? ''
  return unqualifiedName.replace(/^anthropic\./, '')
}

function matchContextWindow(rules: readonly ContextWindowRule[], modelName: string): number | undefined {
  return rules.find(rule => rule.pattern.test(modelName))?.tokens
}

export function suggestedContextWindowTokens(identity: ContextWindowModelIdentity): number | undefined {
  const modelName = normalizedModelName(identity.modelName ?? '')
  if (!modelName) return undefined

  const provider = identity.provider?.trim().toLowerCase() ?? ''
  const isOllama = identity.source === 'local' || provider === 'ollama_cloud'
  if (isOllama) {
    const ollamaMatch = matchContextWindow(OLLAMA_CONTEXT_WINDOW_RULES, modelName)
    if (ollamaMatch !== undefined) return ollamaMatch
  }
  if (provider === 'aliyun') {
    const aliyunMatch = matchContextWindow(ALIYUN_CONTEXT_WINDOW_RULES, modelName)
    if (aliyunMatch !== undefined) return aliyunMatch
  }
  return matchContextWindow(COMMON_CONTEXT_WINDOW_RULES, modelName)
}

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
  if (!isValidContextWindowTokens(value)) {
    throw new Error('Invalid context_window_tokens')
  }
  if (value == null || value === '') return {}
  const numericValue = Number(value)
  if (numericValue <= 0) return {}
  return { context_window_tokens: numericValue }
}

export function contextWindowTokensFromParameters(
  parameters: { context_window_tokens?: number } | undefined,
): number {
  const value = parameters?.context_window_tokens ?? 0
  if (!isValidContextWindowTokens(value)) {
    throw new Error('Invalid context_window_tokens in model response')
  }
  return Number(value)
}
