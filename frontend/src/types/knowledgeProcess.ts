/** Matches backend types.KnowledgeProcessOverrides (snake_case JSON). */

export interface ParserEngineRule {
  file_types: string[]
  engine: string
  xlsx_first_row_as_header?: boolean
}

export interface ChunkingConfigOverride {
  chunk_size?: number
  chunk_overlap?: number
  separators?: string[]
  parser_engine_rules?: ParserEngineRule[]
  enable_parent_child?: boolean
  parent_chunk_size?: number
  child_chunk_size?: number
  strategy?: string
  token_limit?: number
  languages?: string[]
  table_metadata_instructions?: string
}

export interface VLMConfigOverride {
  enabled?: boolean
  model_id?: string
  description_language?: string
  custom_instructions?: string
}

export interface ASRConfigOverride {
  enabled?: boolean
  model_id?: string
  language?: string
}

export interface QuestionGenerationConfigOverride {
  enabled?: boolean
  question_count?: number
  custom_instructions?: string
}

export interface GraphNodeOverride {
  name: string
  attributes?: string[]
}

export interface GraphRelationOverride {
  node1: string
  node2: string
  type: string
}

export interface ExtractConfigOverride {
  enabled?: boolean
  text?: string
  tags?: string[]
  nodes?: GraphNodeOverride[]
  relations?: GraphRelationOverride[]
  custom_instructions?: string
}

export type IngestionAdvisorMode = 'smart' | 'off'

export interface IngestionAdvisorConfig {
  mode: IngestionAdvisorMode
  allow_web_access?: boolean
  allow_read_only_mcp?: boolean
}

export interface IngestionChunkingRecommendation {
  strategy: string
  chunk_size: number
  chunk_overlap: number
  enable_parent_child: boolean
  parent_chunk_size: number
  child_chunk_size: number
  separators: string[]
}

export interface IngestionCandidateScore {
  semantic_integrity: number
  boundary_quality: number
  size_fit: number
  context_efficiency: number
  parent_child: number
  total: number
}

export interface IngestionStructureQuality {
  orphan_table_rows: number
  headerless_continuations: number
  split_atomic_blocks: number
  mixed_sections: number
  oversize_atomic_blocks: number
}

export interface IngestionChunkStructureDescription {
  index: number
  kinds: string[]
  section_depth: number
  has_context: boolean
  table_continuation: boolean
  parent_mapped: boolean
}

export interface IngestionChunkingCandidate {
  id: string
  config: IngestionChunkingRecommendation
  chunk_count: number
  parent_chunk_count: number
  lengths: {
    minimum: number
    maximum: number
    average: number
    p50: number
    p95: number
  }
  structure: {
    present_types: string[]
    heading_retention: number
    faq_retention: number
    table_retention: number
  }
  structure_quality: IngestionStructureQuality
  block_descriptions: IngestionChunkStructureDescription[]
  diagnostics: {
    selected_tier: string
    tier_chain: string[]
    rejected: Array<{ tier: string; reason: string }>
  }
  score: IngestionCandidateScore
  hard_valid: boolean
  violations: string[]
}

export interface IngestionAgentStep {
  round: number
  tool_name: string
  status: string
  duration_ms?: number
  candidate_id?: string
  score?: number
  failure_code?: string
  failure_field?: string
  failure_constraint?: string
}

export interface IngestionAgentRun {
  max_rounds: number
  actual_rounds: number
  available_tools: string[]
  warnings: Array<{ code: string; tool?: string; message: string }>
  steps: IngestionAgentStep[]
  stop_reason: string
}

export interface IngestionAnalysis {
  applied_mode: 'smart' | 'fallback'
  fallback_reason_codes: string[]
  document_kind: string
  confidence: number
  recommended_content_mode: string
  reason_codes: string[]
  summary: string
  recommended_chunking: IngestionChunkingRecommendation
  applied_chunking: IngestionChunkingRecommendation
  model_id: string
  candidates: IngestionChunkingCandidate[]
  selected_candidate_id: string
  selection_reason_codes: string[]
  agent_run: IngestionAgentRun
}

export interface KnowledgeProcessOverrides {
  ingestion_advisor?: IngestionAdvisorConfig
  parser_engine_rules?: ParserEngineRule[]
  chunking_config?: ChunkingConfigOverride
  enable_multimodel?: boolean
  vlm_config?: VLMConfigOverride
  asr_config?: ASRConfigOverride
  question_generation_config?: QuestionGenerationConfigOverride
  graph_enabled?: boolean
  extract_config?: ExtractConfigOverride
  parser_engine_overrides?: Record<string, string>
}
