<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type {
  IngestionAnalysis,
  IngestionChunkingRecommendation,
} from '@/types/knowledgeProcess'

defineProps<{
  analysis: IngestionAnalysis
}>()

const { t } = useI18n()

function localizedEnum(group: 'documentKind' | 'contentMode' | 'strategy', value: string): string {
  const key = `knowledgeStages.analysis.${group}.${value}`
  const localized = t(key)
  return localized === key ? value : localized
}

function formatConfidence(value: number): string {
  return `${(Math.max(0, Math.min(1, value)) * 100).toFixed(0)}%`
}

function formatSeparators(separators: string[]): string {
  return separators.map(separator => JSON.stringify(separator)).join(' · ')
}

interface ComparisonRow {
  key: string
  label: string
  recommended: string
  applied: string
}

function chunkingValue(key: string, input: IngestionChunkingRecommendation): string {
  if (key === 'strategy') return localizedEnum('strategy', input.strategy)
  if (key === 'enable_parent_child') {
    return input.enable_parent_child
      ? t('uploadConfirm.statusOn')
      : t('uploadConfirm.statusOff')
  }
  if (key === 'separators') return formatSeparators(input.separators)
  return String(input[key as keyof IngestionChunkingRecommendation])
}

function comparisonRows(analysis: IngestionAnalysis): ComparisonRow[] {
  const keys = [
    'strategy',
    'chunk_size',
    'chunk_overlap',
    'enable_parent_child',
    'parent_chunk_size',
    'child_chunk_size',
    'separators',
  ]
  return keys.map(key => ({
    key,
    label: t(`knowledgeStages.analysis.chunking.${key}`),
    recommended: chunkingValue(key, analysis.recommended_chunking),
    applied: chunkingValue(key, analysis.applied_chunking),
  }))
}
</script>

<template>
  <section class="analysis-detail" :aria-label="t('knowledgeStages.analysis.title')">
    <div class="analysis-title">{{ t('knowledgeStages.analysis.title') }}</div>
    <p class="analysis-summary">{{ analysis.summary }}</p>
    <dl class="analysis-profile">
      <div>
        <dt>{{ t('knowledgeStages.analysis.documentKindLabel') }}</dt>
        <dd>{{ localizedEnum('documentKind', analysis.document_kind) }}</dd>
      </div>
      <div>
        <dt>{{ t('knowledgeStages.analysis.confidenceLabel') }}</dt>
        <dd class="analysis-mono">{{ formatConfidence(analysis.confidence) }}</dd>
      </div>
      <div>
        <dt>{{ t('knowledgeStages.analysis.contentModeLabel') }}</dt>
        <dd>{{ localizedEnum('contentMode', analysis.recommended_content_mode) }}</dd>
      </div>
      <div>
        <dt>{{ t('knowledgeStages.analysis.modelLabel') }}</dt>
        <dd class="analysis-mono">{{ analysis.model_id }}</dd>
      </div>
      <div>
        <dt>{{ t('knowledgeStages.analysis.promptVersionLabel') }}</dt>
        <dd class="analysis-mono">{{ analysis.prompt_version }}</dd>
      </div>
    </dl>
    <div class="analysis-reasons">
      <span class="analysis-subtitle">{{ t('knowledgeStages.analysis.reasonCodesLabel') }}</span>
      <div class="analysis-reason-list">
        <code v-for="reason in analysis.reason_codes" :key="reason" class="analysis-reason">{{ reason }}</code>
      </div>
    </div>
    <div class="analysis-comparison-wrap">
      <table class="analysis-comparison">
        <caption>{{ t('knowledgeStages.analysis.chunkingComparison') }}</caption>
        <thead>
          <tr>
            <th scope="col">{{ t('knowledgeStages.analysis.parameter') }}</th>
            <th scope="col">{{ t('knowledgeStages.analysis.recommended') }}</th>
            <th scope="col">{{ t('knowledgeStages.analysis.applied') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in comparisonRows(analysis)" :key="row.key">
            <th scope="row">{{ row.label }}</th>
            <td>{{ row.recommended }}</td>
            <td>{{ row.applied }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<style scoped lang="less">
.analysis-detail {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
  background: var(--td-bg-color-secondarycontainer);
}

.analysis-title {
  color: var(--td-text-color-secondary);
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.5px;
  text-transform: uppercase;
}

.analysis-summary {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: 13px;
  line-height: 1.6;
}

.analysis-profile {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1px;
  margin: 0;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
  background: var(--td-component-stroke);
}

.analysis-profile>div {
  min-width: 0;
  padding: 8px 10px;
  background: var(--td-bg-color-container);
}

.analysis-profile dt,
.analysis-profile dd {
  margin: 0;
}

.analysis-profile dt {
  margin-bottom: 3px;
  color: var(--td-text-color-placeholder);
  font-size: 10px;
}

.analysis-profile dd {
  overflow-wrap: anywhere;
  color: var(--td-text-color-primary);
  font-size: 12px;
}

.analysis-mono,
.analysis-reason {
  font-family: var(--app-font-family-mono);
}

.analysis-reasons {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.analysis-subtitle {
  color: var(--td-text-color-secondary);
  font-size: 11px;
  font-weight: 500;
}

.analysis-reason-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.analysis-reason {
  padding: 2px 6px;
  border-radius: var(--td-radius-default);
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-size: 10px;
}

.analysis-comparison-wrap {
  overflow-x: auto;
}

.analysis-comparison {
  width: 100%;
  min-width: 520px;
  border-collapse: collapse;
  background: var(--td-bg-color-container);
  font-size: 11px;
}

.analysis-comparison caption {
  padding: 0 0 6px;
  color: var(--td-text-color-secondary);
  font-weight: 500;
  text-align: left;
}

.analysis-comparison th,
.analysis-comparison td {
  padding: 7px 9px;
  border: 1px solid var(--td-component-stroke);
  text-align: left;
  overflow-wrap: anywhere;
}

.analysis-comparison thead th {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-weight: 500;
}

.analysis-comparison tbody th {
  color: var(--td-text-color-secondary);
  font-weight: 500;
}
</style>
