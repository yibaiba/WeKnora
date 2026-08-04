<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type {
  IngestionAgentStep,
  IngestionAnalysis,
  IngestionCandidateScore,
  IngestionChunkingCandidate,
} from '@/types/knowledgeProcess'

const props = defineProps<{
  analysis: IngestionAnalysis
}>()

const { t } = useI18n()

const scoreDimensions: Array<{ key: keyof IngestionCandidateScore; weight: number }> = [
  { key: 'structure_integrity', weight: 40 },
  { key: 'chunk_size_balance', weight: 25 },
  { key: 'boundary_quality', weight: 15 },
  { key: 'overlap_efficiency', weight: 10 },
  { key: 'parent_child', weight: 10 },
]

const phaseKeys = [
  'analyze_document',
  'readonly_tools',
  'preview_candidates',
  'evaluate_and_refine',
  'submit_decision',
] as const

function formatScore(value: number, maximum = 100): string {
  const safe = Number.isFinite(value) ? Math.max(0, Math.min(maximum, value)) : 0
  return `${safe.toFixed(1)} / ${maximum}`
}

function isSelected(candidate: IngestionChunkingCandidate): boolean {
  return candidate.id === props.analysis.selected_candidate_id
}

function candidateTitle(candidate: IngestionChunkingCandidate, index: number): string {
  if (isSelected(candidate)) return t('knowledgeStages.analysis.candidateSelected', { n: index + 1 })
  return t('knowledgeStages.analysis.candidateNumber', { n: index + 1 })
}

function localizedTool(name: string): string {
  const key = `knowledgeStages.analysis.tool.${name}`
  const localized = t(key)
  return localized === key ? name : localized
}

function localizedRunStatus(status: string): string {
  const key = `knowledgeStages.status.${status}`
  const localized = t(key)
  return localized === key ? status : localized
}

function phaseStatus(phase: typeof phaseKeys[number]): string {
  const steps = props.analysis.agent_run.steps || []
  if (phase === 'analyze_document') return 'done'
  if (phase === 'evaluate_and_refine') return props.analysis.candidates.length > 0 ? 'done' : 'pending'
  if (phase === 'submit_decision') return props.analysis.selected_candidate_id ? 'done' : 'pending'
  const matching = steps.filter(step => stepPhase(step) === phase)
  if (matching.length === 0) return phase === 'readonly_tools' ? 'skipped' : 'pending'
  return matching.some(step => step.status === 'failed') ? 'failed' : 'done'
}

function stepPhase(step: IngestionAgentStep): typeof phaseKeys[number] {
  if (step.tool_name === 'inspect_ingestion_document') return 'analyze_document'
  if (step.tool_name === 'preview_ingestion_chunking') return 'preview_candidates'
  if (step.tool_name === 'submit_ingestion_decision') return 'submit_decision'
  if (step.tool_name === 'thinking') return 'evaluate_and_refine'
  return 'readonly_tools'
}
</script>

<template>
  <div class="agent-run-detail">
    <section class="agent-section" :aria-label="t('knowledgeStages.analysis.agentRunTitle')">
      <div class="agent-section-heading">
        <h3>{{ t('knowledgeStages.analysis.agentRunTitle') }}</h3>
        <span class="agent-rounds">
          {{ t('knowledgeStages.analysis.rounds', {
            actual: analysis.agent_run.actual_rounds,
            max: analysis.agent_run.max_rounds,
          }) }}
        </span>
      </div>
      <ol class="agent-phases">
        <li v-for="phase in phaseKeys" :key="phase" class="agent-phase">
          <span class="agent-phase-dot" :class="`agent-phase-dot--${phaseStatus(phase)}`" aria-hidden="true" />
          <span>{{ t(`knowledgeStages.analysis.phase.${phase}`) }}</span>
          <span class="agent-phase-status">{{ localizedRunStatus(phaseStatus(phase)) }}</span>
        </li>
      </ol>
    </section>

    <section v-if="analysis.candidates.length > 0" class="agent-section">
      <h3>{{ t('knowledgeStages.analysis.candidatesTitle') }}</h3>
      <div v-if="analysis.selection_reason_codes.length > 0" class="selection-reasons">
        <span>{{ t('knowledgeStages.analysis.selectionReasonCodesLabel') }}</span>
        <code v-for="reason in analysis.selection_reason_codes" :key="reason">{{ reason }}</code>
      </div>
      <div class="candidate-list">
        <article
          v-for="(candidate, index) in analysis.candidates"
          :key="candidate.id"
          class="candidate-card"
          :class="{ 'candidate-card--selected': isSelected(candidate) }"
        >
          <header class="candidate-head">
            <div>
              <strong>{{ candidateTitle(candidate, index) }}</strong>
              <code>{{ candidate.id }}</code>
            </div>
            <div class="candidate-score">
              <span>{{ formatScore(candidate.score.total) }}</span>
              <meter
                min="0"
                max="100"
                :value="candidate.score.total"
                :aria-label="t('knowledgeStages.analysis.totalScore')"
              />
            </div>
          </header>
          <dl class="candidate-metrics">
            <div>
              <dt>{{ t('knowledgeStages.analysis.chunkCount') }}</dt>
              <dd>{{ candidate.chunk_count }}</dd>
            </div>
            <div>
              <dt>{{ t('knowledgeStages.analysis.lengthP50P95') }}</dt>
              <dd>{{ candidate.lengths.p50 }} / {{ candidate.lengths.p95 }}</dd>
            </div>
            <div>
              <dt>{{ t('knowledgeStages.analysis.selectedTier') }}</dt>
              <dd>{{ candidate.diagnostics.selected_tier || '—' }}</dd>
            </div>
            <div>
              <dt>{{ t('knowledgeStages.analysis.chunkConfigShort') }}</dt>
              <dd>{{ candidate.config.strategy }} · {{ candidate.config.chunk_size }} / {{ candidate.config.chunk_overlap }}</dd>
            </div>
          </dl>
          <table class="candidate-score-table">
            <caption>{{ t('knowledgeStages.analysis.scoreBreakdown') }}</caption>
            <tbody>
              <tr v-for="dimension in scoreDimensions" :key="dimension.key">
                <th scope="row">{{ t(`knowledgeStages.analysis.score.${dimension.key}`) }}</th>
                <td>{{ formatScore(candidate.score[dimension.key], dimension.weight) }}</td>
              </tr>
            </tbody>
          </table>
          <p v-if="candidate.violations.length > 0" class="candidate-violations" role="alert">
            {{ candidate.violations.join(' · ') }}
          </p>
        </article>
      </div>
    </section>

    <section v-if="analysis.agent_run.steps.length > 0" class="agent-section">
      <h3>{{ t('knowledgeStages.analysis.toolStepsTitle') }}</h3>
      <ol class="tool-step-list">
        <li v-for="(step, index) in analysis.agent_run.steps" :key="`${step.round}-${step.tool_name}-${index}`">
          <span class="tool-step-round">R{{ step.round }}</span>
          <span class="tool-step-name">{{ localizedTool(step.tool_name) }}</span>
          <span class="tool-step-status">{{ localizedRunStatus(step.status) }}</span>
          <span v-if="step.candidate_id" class="tool-step-candidate">{{ step.candidate_id }}</span>
          <span v-if="step.score !== undefined" class="tool-step-score">{{ step.score.toFixed(1) }}</span>
          <span v-if="step.duration_ms !== undefined" class="tool-step-duration">{{ step.duration_ms }}ms</span>
        </li>
      </ol>
    </section>

    <section v-if="analysis.agent_run.warnings.length > 0" class="agent-section agent-warnings">
      <h3>{{ t('knowledgeStages.analysis.warningsTitle') }}</h3>
      <ul>
        <li v-for="warning in analysis.agent_run.warnings" :key="`${warning.code}-${warning.tool || ''}`">
          <code>{{ warning.tool || warning.code }}</code>
          <span>{{ warning.message }}</span>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped lang="less">
.agent-run-detail,
.agent-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.agent-section {
  padding-top: 10px;
  border-top: 1px solid var(--td-component-stroke);
}

.agent-section h3 {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-weight: 600;
}

.agent-section-heading,
.candidate-head,
.tool-step-list li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.agent-rounds,
.agent-phase-status,
.tool-step-status,
.tool-step-duration {
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.agent-phases,
.tool-step-list,
.agent-warnings ul {
  margin: 0;
  padding: 0;
  list-style: none;
}

.selection-reasons {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.selection-reasons code {
  padding: 2px 6px;
  border-radius: var(--td-radius-default);
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  overflow-wrap: anywhere;
}

.agent-phase {
  display: grid;
  grid-template-columns: 8px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  min-height: 28px;
  font-size: 12px;
}

.agent-phase-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--td-text-color-placeholder);
}

.agent-phase-dot--done { background: var(--td-success-color); }
.agent-phase-dot--failed { background: var(--td-error-color); }
.agent-phase-dot--pending,
.agent-phase-dot--skipped { background: var(--td-bg-color-component-active); }

.candidate-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(230px, 1fr));
  gap: 8px;
}

.candidate-card {
  min-width: 0;
  padding: 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
  background: var(--td-bg-color-container);
}

.candidate-card--selected {
  border-color: var(--td-brand-color);
  box-shadow: inset 3px 0 0 var(--td-brand-color);
}

.candidate-head > div:first-child {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.candidate-head code,
.tool-step-candidate {
  overflow-wrap: anywhere;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.candidate-score {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}

.candidate-score meter { width: 72px; }

.candidate-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px;
  margin: 10px 0;
}

.candidate-metrics div {
  min-width: 0;
  padding: 6px;
  background: var(--td-bg-color-secondarycontainer);
}

.candidate-metrics dt,
.candidate-metrics dd { margin: 0; }
.candidate-metrics dt { color: var(--td-text-color-placeholder); font-size: 12px; }
.candidate-metrics dd { overflow-wrap: anywhere; font-size: 12px; }

.candidate-score-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

.candidate-score-table caption {
  padding-bottom: 4px;
  color: var(--td-text-color-secondary);
  text-align: left;
}

.candidate-score-table th,
.candidate-score-table td { padding: 3px 0; text-align: left; }
.candidate-score-table th { font-weight: 400; color: var(--td-text-color-secondary); }
.candidate-score-table td { text-align: right; font-variant-numeric: tabular-nums; }

.candidate-violations {
  margin: 8px 0 0;
  color: var(--td-error-color);
  font-size: 12px;
  line-height: 1.5;
}

.tool-step-list li {
  justify-content: flex-start;
  min-height: 28px;
  font-size: 12px;
}

.tool-step-round,
.tool-step-score { font-family: var(--app-font-family-mono); }
.tool-step-name { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.tool-step-candidate { max-width: 110px; }

.agent-warnings li {
  display: flex;
  gap: 8px;
  padding: 6px 0;
  color: var(--td-warning-color);
  font-size: 12px;
  line-height: 1.5;
}

.agent-warnings code { overflow-wrap: anywhere; }

@media (max-width: 640px) {
  .candidate-list { grid-template-columns: 1fr; }
  .tool-step-list li { flex-wrap: wrap; }
}
</style>
