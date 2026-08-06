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
  'semantic_integrity',
  'boundary_quality',
  'size_fit',
  'context_efficiency',
  'parent_child',
  'total',
] as const
const LEGACY_SCORE_KEYS = [
  'structure_integrity',
  'chunk_size_balance',
  'boundary_quality',
  'overlap_efficiency',
  'parent_child',
  'total',
] as const
const STRUCTURE_QUALITY_KEYS = [
  'orphan_table_rows',
  'headerless_continuations',
  'split_atomic_blocks',
  'mixed_sections',
  'oversize_atomic_blocks',
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

function normalizeCandidateScore(candidate: Record<string, unknown>): IngestionChunkingCandidate['score'] | null {
  const score = candidate.score as Record<string, unknown> | undefined
  if (!score) return null
  if (SCORE_KEYS.every(key => isFiniteNumber(score[key]))) {
    return score as unknown as IngestionChunkingCandidate['score']
  }
  if (!LEGACY_SCORE_KEYS.every(key => isFiniteNumber(score[key]))) return null
  return {
    semantic_integrity: score.structure_integrity as number,
    boundary_quality: score.boundary_quality as number,
    size_fit: score.chunk_size_balance as number,
    context_efficiency: score.overlap_efficiency as number,
    parent_child: score.parent_child as number,
    total: score.total as number,
  }
}

function normalizeStructureQuality(value: unknown): IngestionChunkingCandidate['structure_quality'] {
  const quality = value && typeof value === 'object' ? value as Record<string, unknown> : {}
  return Object.fromEntries(STRUCTURE_QUALITY_KEYS.map(key => [
    key,
    isFiniteNumber(quality[key]) ? quality[key] : 0,
  ])) as unknown as IngestionChunkingCandidate['structure_quality']
}

function isBlockDescription(value: unknown): boolean {
  if (!value || typeof value !== 'object') return false
  const description = value as Record<string, unknown>
  return isFiniteNumber(description.index) && isStringArray(description.kinds) &&
    isFiniteNumber(description.section_depth) && typeof description.has_context === 'boolean' &&
    typeof description.table_continuation === 'boolean' && typeof description.parent_mapped === 'boolean'
}

function normalizeIngestionCandidate(value: unknown): IngestionChunkingCandidate | null {
  if (!value || typeof value !== 'object') return null
  const candidate = value as Record<string, unknown>
  const valid = typeof candidate.id === 'string' &&
    isChunkingRecommendation(candidate.config) &&
    isFiniteNumber(candidate.chunk_count) &&
    isFiniteNumber(candidate.parent_chunk_count) &&
    typeof candidate.hard_valid === 'boolean' &&
    isStringArray(candidate.violations) &&
    hasCandidateMetrics(candidate) &&
    hasCandidateDiagnostics(candidate)
  const score = normalizeCandidateScore(candidate)
  if (!valid || !score) return null
  return {
    ...candidate,
    score,
    structure_quality: normalizeStructureQuality(candidate.structure_quality),
    block_descriptions: Array.isArray(candidate.block_descriptions)
      ? candidate.block_descriptions.filter(isBlockDescription)
      : [],
  } as unknown as IngestionChunkingCandidate
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
    (step.score === undefined || isFiniteNumber(step.score)) &&
    (step.failure_code === undefined || typeof step.failure_code === 'string') &&
    (step.failure_field === undefined || typeof step.failure_field === 'string') &&
    (step.failure_constraint === undefined || typeof step.failure_constraint === 'string')
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
    typeof analysis.model_id === 'string'
}

export function asIngestionAnalysis(value: unknown): IngestionAnalysis | null {
  if (!value || typeof value !== 'object') return null
  const analysis = value as Record<string, unknown>
  if (!hasAnalysisProfile(analysis)) return null
  if (!isChunkingRecommendation(analysis.recommended_chunking)) return null
  if (!isChunkingRecommendation(analysis.applied_chunking)) return null
  const candidates = Array.isArray(analysis.candidates)
    ? analysis.candidates.map(normalizeIngestionCandidate).filter(candidate => candidate !== null)
    : []
  return {
    ...analysis,
    applied_mode: analysis.applied_mode === 'fallback' ? 'fallback' : 'smart',
    fallback_reason_codes: isStringArray(analysis.fallback_reason_codes)
      ? analysis.fallback_reason_codes
      : [],
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
