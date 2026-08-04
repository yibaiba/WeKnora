import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'knowledge-processing-timeline.vue'), 'utf8')
const analysisDetail = readFileSync(join(here, 'knowledge-ingestion-analysis-detail.vue'), 'utf8')
const agentRunDetail = readFileSync(join(here, 'knowledge-ingestion-agent-run-detail.vue'), 'utf8')

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
    'prompt_version',
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
  assert.match(retryBuilder, /mode,\s*prompt_version: 'v1'/s)
  assert.match(source, /submitRetry\('smart', buildAdvisorRetryOverrides\('smart'\)\)/)
  assert.match(source, /submitRetry\('off', buildAdvisorRetryOverrides\('off'\)\)/)
})

test('shows both recovery actions only for document analysis failures', () => {
  assert.match(source, /error_code === 'DOCUMENT_ANALYSIS_FAILED'/)
  assert.match(source, /errorCode\.startsWith\('INGESTION_'\)/)
  assert.match(source, /stage\.status === 'failed' && !!stage\.span_id/)
  assert.match(source, /v-if="documentAnalysisFailed"/)
  assert.match(source, /knowledgeStages\.smartRetry/)
  assert.match(source, /knowledgeStages\.kbRetry/)
})

test('localizes document analysis child spans from their redacted phase identifiers', () => {
  const labels = source.slice(source.indexOf('function rowLabel'), source.indexOf('function rowKindLabel'))

  assert.match(labels, /row\.node\.name\.startsWith\('document_analysis\.'\)/)
  assert.match(labels, /knowledgeStages\.analysis\.phase\.\$\{phase\}/)
})

test('does not reuse stale analysis metadata for a skipped latest attempt', () => {
  const selectedAnalysis = source.slice(
    source.indexOf('const selectedIngestionAnalysis'),
    source.indexOf('watch([selectedSpanId, detailTab]'),
  )

  assert.match(selectedAnalysis, /row\.node\.status === 'skipped'/)
  assert.match(selectedAnalysis, /return viewingLatestAttempt\.value \? ingestionAnalysis\.value : null/)
})
