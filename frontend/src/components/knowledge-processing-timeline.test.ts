import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'knowledge-processing-timeline.vue'), 'utf8')
const analysisDetail = readFileSync(join(here, 'knowledge-ingestion-analysis-detail.vue'), 'utf8')
const agentRunDetail = readFileSync(join(here, 'knowledge-ingestion-agent-run-detail.vue'), 'utf8')
const analysisNormalizer = readFileSync(join(here, '../utils/ingestionAnalysis.ts'), 'utf8')
const localeSources = ['en-US', 'zh-CN', 'ru-RU', 'ko-KR'].map(locale =>
  readFileSync(join(here, `../i18n/locales/${locale}.ts`), 'utf8'),
)

test('orders document analysis between parsing and chunking', () => {
  assert.match(
    source,
    /const STAGES = \['docreader', 'document_analysis', 'chunking', 'embedding', 'multimodal', 'postprocess'\]/,
  )
})

test('marks missing analysis as skipped for legacy traces that reached downstream stages', () => {
  const compatibility = source.slice(
    source.indexOf('function shouldShowAnalysisAsSkipped'),
    source.indexOf('const currentStageLabel'),
  )

  assert.match(compatibility, /\['chunking', 'embedding', 'multimodal', 'postprocess'\]/)
  assert.match(compatibility, /downstreamStarted \|\| isHardTerminal/)
})

test('counts skipped stages when determining the current stage', () => {
  const currentStageIndex = source.slice(
    source.indexOf('const currentStageIndex'),
    source.indexOf('function formatDuration'),
  )

  assert.match(currentStageIndex, /status === 'done' \|\| s\.status === 'skipped'/)
  assert.match(currentStageIndex, /Math\.min\(traversed \+ 1, stages\.value\.length\)/)
})

test('counts skipped stages in the completed stage total', () => {
  const stagesStatDisplay = source.slice(
    source.indexOf('const stagesStatDisplay'),
    source.indexOf('const postprocessTaskStats'),
  )

  assert.match(stagesStatDisplay, /status === 'done' \|\| s\.status === 'skipped'/)
  assert.match(stagesStatDisplay, /value: `\$\{completedCount\}\/\$\{total\}`/)
})

test('renders every persisted ingestion analysis field and compares chunking values', () => {
  for (const field of [
    'document_kind',
    'confidence',
    'recommended_content_mode',
    'reason_codes',
    'summary',
    'recommended_chunking',
    'applied_chunking',
    'model_id',
    'candidates',
    'selected_candidate_id',
    'selection_reason_codes',
    'agent_run',
  ]) {
    assert.match(`${analysisDetail}\n${agentRunDetail}`, new RegExp(`analysis\\.${field}`))
  }
  assert.match(analysisDetail, /comparisonRows\(analysis\)/)
  assert.match(analysisDetail, /scope="col"/)
  assert.match(analysisDetail, /scope="row"/)
})

test('renders only structured ingestion phases, candidate scores, and redacted tool summaries', () => {
  for (const phase of [
    'analyze_document',
    'readonly_tools',
    'preview_candidates',
    'evaluate_and_refine',
    'submit_decision',
  ]) {
    assert.match(agentRunDetail, new RegExp(phase))
  }
  for (const score of [
    'structure_integrity',
    'chunk_size_balance',
    'boundary_quality',
    'overlap_efficiency',
    'parent_child',
  ]) {
    assert.match(agentRunDetail, new RegExp(score))
  }
  assert.match(agentRunDetail, /analysis\.agent_run\.steps/)
  assert.match(agentRunDetail, /analysis\.candidates/)
  assert.doesNotMatch(agentRunDetail, /thought|reasoning_content|raw_arguments|raw_output/i)
})

test('validates persisted candidate and agent-run shapes before rendering', () => {
  for (const field of [
    'minimum',
    'maximum',
    'average',
    'p50',
    'p95',
    'tier_chain',
    'rejected',
    'available_tools',
    'warnings',
    'steps',
  ]) {
    assert.match(analysisNormalizer, new RegExp(field))
  }
  assert.match(analysisNormalizer, /Number\.isFinite/)
  assert.match(analysisNormalizer, /run\.warnings\.filter\(isAgentWarning\)/)
  assert.match(analysisNormalizer, /run\.steps\.filter\(isAgentStep\)/)
  assert.doesNotMatch(analysisNormalizer, /thought|reasoning_content|raw_arguments|raw_output/i)
})

test('localizes every structured ingestion failure category in all supported locales', () => {
  const codes = [
    'INGESTION_DOCUMENT_ANALYSIS_FAILED',
    'INGESTION_MODEL_UNAVAILABLE',
    'INGESTION_MODEL_TOOL_CALLING_UNSUPPORTED',
    'INGESTION_CORE_TOOL_FAILED',
    'INGESTION_CANDIDATE_INVALID',
    'INGESTION_TOOL_FAILED',
    'INGESTION_TOOL_ARGUMENTS_INVALID',
    'INGESTION_CANDIDATE_PREVIEW_FAILED',
    'INGESTION_CANDIDATE_LIMIT_REACHED',
    'INGESTION_DECISION_INVALID',
    'INGESTION_AGENT_MAX_ROUNDS',
    'INGESTION_DECISION_NOT_SUBMITTED',
    'INGESTION_AGENT_EXECUTION_FAILED',
  ]

  for (const locale of localeSources) {
    for (const code of codes) {
      assert.match(locale, new RegExp(`${code}:`))
      assert.match(locale, new RegExp(`${code}_SUGGESTION:`))
    }
  }
})

test('builds explicit smart and knowledge-base retry payloads without mutating stored overrides', () => {
  const retryBuilder = source.slice(
    source.indexOf('function buildAdvisorRetryOverrides'),
    source.indexOf('async function submitRetry'),
  )

  assert.match(retryBuilder, /source \? \{ \.\.\.source \} : \{\}/)
  assert.match(retryBuilder, /if \(mode === 'off'\) delete overrides\.chunking_config/)
  assert.match(retryBuilder, /const previousAdvisor = source\?\.ingestion_advisor/)
  assert.match(retryBuilder, /previousAdvisor\?\.allow_web_access/)
  assert.match(retryBuilder, /previousAdvisor\?\.allow_read_only_mcp/)
  assert.match(retryBuilder, /overrides\.ingestion_advisor = \{\s*mode,/s)
  assert.doesNotMatch(retryBuilder, /prompt_version/)
  assert.match(source, /submitRetry\('smart', buildAdvisorRetryOverrides\('smart'\)\)/)
  assert.match(source, /submitRetry\('off', buildAdvisorRetryOverrides\('off'\)\)/)
})

test('shows both recovery actions only for document analysis failures', () => {
  assert.match(source, /errorCode === 'DOCUMENT_ANALYSIS_FAILED'/)
  assert.match(source, /errorCode\.startsWith\('INGESTION_'\)/)
  assert.match(source, /stage\.status === 'failed' && !!stage\.span_id/)
  assert.match(source, /v-if="documentAnalysisFailed"/)
  assert.match(source, /knowledgeStages\.smartRetry/)
  assert.match(source, /knowledgeStages\.kbRetry/)
})

test('localizes document analysis child spans from their redacted phase identifiers', () => {
  const labels = source.slice(source.indexOf('function rowLabel'), source.indexOf('function rowKindLabel'))

  assert.match(source, /function documentAnalysisPhase/)
  assert.match(source, /const phaseMatch = \/\^document_analysis/)
  assert.match(labels, /documentAnalysisPhase\(row\.node\.name\)/)
  assert.match(labels, /knowledgeStages\.analysis\.phase\.\$\{phase\}/)
  assert.doesNotMatch(labels, /row\.node\.input/)
  for (const locale of localeSources) {
    assert.match(locale, /map_document:/)
    assert.match(locale, /reduce_document:/)
  }
})

test('does not reuse stale analysis metadata for a skipped latest attempt', () => {
  const selectedAnalysis = source.slice(
    source.indexOf('const selectedIngestionAnalysis'),
    source.indexOf('watch([selectedSpanId, detailTab]'),
  )

  assert.match(selectedAnalysis, /row\.node\.status === 'skipped'/)
  assert.match(selectedAnalysis, /return viewingLatestAttempt\.value \? ingestionAnalysis\.value : null/)
})
