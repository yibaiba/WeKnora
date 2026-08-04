import type {
  IngestionAgentRun,
  IngestionAgentStep,
  IngestionAnalysis,
  IngestionChunkingCandidate,
  IngestionChunkingRecommendation,
} from '@/types/knowledgeProcess'

const CANDIDATE_LENGTH_KEYS = ['minimum', 'maximum', 'average', 'p50', 'p95'] as const
const STRUCTURE_SCORE_KEYS = ['heading_retention', 'faq_retention', 'table_retention'] as const
const SCORE_KEYS = [
  'structure_integrity',
  'chunk_size_balance',
  'boundary_quality',
  'overlap_efficiency',
  'parent_child',
  'total',
] as const

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every(item => typeof item === 'string')
}

function isChunkingRecommendation(value: unknown): value is IngestionChunkingRecommendation {
  if (!value || typeof value !== 'object') return false
  const chunking = value as Record<string, unknown>
  return typeof chunking.strategy === 'string' &&
    isFiniteNumber(chunking.chunk_size) &&
    isFiniteNumber(chunking.chunk_overlap) &&
    typeof chunking.enable_parent_child === 'boolean' &&
    isFiniteNumber(chunking.parent_chunk_size) &&
    isFiniteNumber(chunking.child_chunk_size) &&
    isStringArray(chunking.separators)
}

function hasCandidateMetrics(candidate: Record<string, unknown>): boolean {
  const lengths = candidate.lengths as Record<string, unknown> | undefined
  const structure = candidate.structure as Record<string, unknown> | undefined
  if (!lengths || !structure) return false
  return CANDIDATE_LENGTH_KEYS.every(key => isFiniteNumber(lengths[key])) &&
    isStringArray(structure.present_types) &&
    STRUCTURE_SCORE_KEYS.every(key => isFiniteNumber(structure[key]))
}

function hasCandidateDiagnostics(candidate: Record<string, unknown>): boolean {
  const diagnostics = candidate.diagnostics as Record<string, unknown> | undefined
  if (!diagnostics || typeof diagnostics.selected_tier !== 'string') return false
  if (!isStringArray(diagnostics.tier_chain) || !Array.isArray(diagnostics.rejected)) return false
  return diagnostics.rejected.every((item) => {
    if (!item || typeof item !== 'object') return false
    const rejected = item as Record<string, unknown>
    return typeof rejected.tier === 'string' && typeof rejected.reason === 'string'
  })
}

function hasCandidateScore(candidate: Record<string, unknown>): boolean {
  const score = candidate.score as Record<string, unknown> | undefined
  return !!score && SCORE_KEYS.every(key => isFiniteNumber(score[key]))
}

function isIngestionCandidate(value: unknown): value is IngestionChunkingCandidate {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Record<string, unknown>
  return typeof candidate.id === 'string' &&
    isChunkingRecommendation(candidate.config) &&
    isFiniteNumber(candidate.chunk_count) &&
    isFiniteNumber(candidate.parent_chunk_count) &&
    typeof candidate.hard_valid === 'boolean' &&
    isStringArray(candidate.violations) &&
    hasCandidateMetrics(candidate) &&
    hasCandidateDiagnostics(candidate) &&
    hasCandidateScore(candidate)
}

function isAgentWarning(value: unknown): value is IngestionAgentRun['warnings'][number] {
  if (!value || typeof value !== 'object') return false
  const warning = value as Record<string, unknown>
  return typeof warning.code === 'string' &&
    typeof warning.message === 'string' &&
    (warning.tool === undefined || typeof warning.tool === 'string')
}

function isAgentStep(value: unknown): value is IngestionAgentStep {
  if (!value || typeof value !== 'object') return false
  const step = value as Record<string, unknown>
  return isFiniteNumber(step.round) &&
    typeof step.tool_name === 'string' &&
    typeof step.status === 'string' &&
    (step.duration_ms === undefined || isFiniteNumber(step.duration_ms)) &&
    (step.candidate_id === undefined || typeof step.candidate_id === 'string') &&
    (step.score === undefined || isFiniteNumber(step.score))
}

function normalizeAgentRun(value: unknown): IngestionAgentRun {
  if (!value || typeof value !== 'object') {
    return { max_rounds: 4, actual_rounds: 0, available_tools: [], warnings: [], steps: [], stop_reason: '' }
  }
  const run = value as Record<string, unknown>
  return {
    max_rounds: isFiniteNumber(run.max_rounds) ? run.max_rounds : 4,
    actual_rounds: isFiniteNumber(run.actual_rounds) ? run.actual_rounds : 0,
    available_tools: isStringArray(run.available_tools) ? run.available_tools : [],
    warnings: Array.isArray(run.warnings) ? run.warnings.filter(isAgentWarning) : [],
    steps: Array.isArray(run.steps) ? run.steps.filter(isAgentStep) : [],
    stop_reason: typeof run.stop_reason === 'string' ? run.stop_reason : '',
  }
}

function hasAnalysisProfile(analysis: Record<string, unknown>): boolean {
  return typeof analysis.document_kind === 'string' &&
    isFiniteNumber(analysis.confidence) &&
    typeof analysis.recommended_content_mode === 'string' &&
    isStringArray(analysis.reason_codes) &&
    typeof analysis.summary === 'string' &&
    typeof analysis.model_id === 'string' &&
    typeof analysis.prompt_version === 'string'
}

export function asIngestionAnalysis(value: unknown): IngestionAnalysis | null {
  if (!value || typeof value !== 'object') return null
  const analysis = value as Record<string, unknown>
  if (!hasAnalysisProfile(analysis)) return null
  if (!isChunkingRecommendation(analysis.recommended_chunking)) return null
  if (!isChunkingRecommendation(analysis.applied_chunking)) return null
  const candidates = Array.isArray(analysis.candidates) && analysis.candidates.every(isIngestionCandidate)
    ? analysis.candidates
    : []
  return {
    ...analysis,
    candidates,
    selected_candidate_id: typeof analysis.selected_candidate_id === 'string'
      ? analysis.selected_candidate_id
      : '',
    selection_reason_codes: isStringArray(analysis.selection_reason_codes)
      ? analysis.selection_reason_codes
      : [],
    agent_run: normalizeAgentRun(analysis.agent_run),
  } as unknown as IngestionAnalysis
}
