import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const dialog = readFileSync(new URL('./UploadConfirmDialog.vue', import.meta.url), 'utf8')
const host = readFileSync(new URL('../../../components/UploadConfirmHost.vue', import.meta.url), 'utf8')
const knowledgeBase = readFileSync(new URL('../KnowledgeBase.vue', import.meta.url), 'utf8')
const platform = readFileSync(new URL('../../platform/index.vue', import.meta.url), 'utf8')

test('selects multiple document tags and returns them with the confirmation result', () => {
  assert.match(host, /:tag-ids="uploadConfirmStore\.tagIds"/)
  assert.match(dialog, /v-model="selectedTagIds"/)
  assert.match(dialog, /multiple/)
  assert.match(dialog, /listKnowledgeTags\(kbId, \{ page: 1, page_size: 1000 \}\)/)
  assert.match(dialog, /tagIds: \[\.\.\.selectedTagIds\.value\]/)
})

test('uses confirmed tags for file and URL imports instead of reading the list filter at upload time', () => {
  assert.match(knowledgeBase, /const tagIds = result\.tagIds \|\| \[\]/)
  assert.match(knowledgeBase, /executeUploadBatch\(files, \{ processConfig, tagIds \}\)/)
  assert.match(knowledgeBase, /executeUrlImport\(url, urlProcessConfig, tagIds\)/)
  assert.doesNotMatch(
    knowledgeBase,
    /const tagIdsToUpload = selectedTagIds\.value\.length > 0 \? \[\.\.\.selectedTagIds\.value\] : undefined/,
  )
})

test('routes global knowledge file drops through the upload confirmation flow', () => {
  assert.match(platform, /weknora:knowledge-file-drop/)
  assert.match(knowledgeBase, /handleKnowledgeFileDrop/)
  assert.match(knowledgeBase, /handleUploadSourceFiles\(files\)/)
})

test('uses section navigation with inline chunking controls and advanced options grouped', () => {
  assert.match(dialog, /v-model="uiState\.chunkingConfig\.strategy"/)
  assert.match(dialog, /v-model="uiState\.chunkingConfig\.chunkSize"/)
  assert.match(dialog, /v-model="uiState\.chunkingConfig\.chunkOverlap"/)
  assert.match(dialog, /chunkingMoreOpen/)
  assert.match(dialog, /activeSection === 'graph'/)
  assert.match(dialog, /class="files-panel"/)
  assert.match(dialog, /statusFull/)
  assert.match(dialog, /data-section="multimodal"/)
  assert.doesNotMatch(dialog, /<KBChunkingSettings/)
})

test('defaults new file uploads to smart analysis while preserving legacy reparse behavior', () => {
  const initializer = dialog.slice(
    dialog.indexOf('function initializeIngestionAdvisorMode'),
    dialog.indexOf('async function loadModels'),
  )

  assert.match(initializer, /props\.mode === 'file'/)
  assert.match(initializer, /ingestionAdvisorMode = 'smart'/)
  assert.match(initializer, /processOverrides\?\.ingestion_advisor\?\.mode === 'smart'/)
  assert.match(initializer, /ingestionAdvisorMode = 'off'/)
})

test('only opts eligible file uploads and file reparses into the advisor', () => {
  const availability = dialog.slice(
    dialog.indexOf('const advisorModeAvailable'),
    dialog.indexOf('const isSmartAnalysis'),
  )
  const payload = dialog.slice(
    dialog.indexOf('function buildProcessOverrides'),
    dialog.indexOf('function applyOverridesToState'),
  )

  assert.match(availability, /props\.mode === 'file'.*localFiles\.value\.length > 0/s)
  assert.match(availability, /props\.mode !== 'reparse'/)
  assert.match(availability, /source\?\.knowledgeType === 'file'/)
  assert.match(availability, /!source\.isDatasource/)
  assert.doesNotMatch(availability, /fileType|html/)
  assert.match(payload, /if \(advisorModeAvailable\.value\)/)
  assert.match(payload, /mode: state\.ingestionAdvisorMode/)
  assert.match(payload, /prompt_version: 'v1'/)
  assert.match(payload, /state\.allowSmartWebAccess/)
  assert.match(payload, /allow_web_access: true/)
  assert.match(payload, /state\.allowSmartReadOnlyMcp/)
  assert.match(payload, /allow_read_only_mcp: true/)
})

test('keeps external smart tools opt-in and explains source-content transfer', () => {
  const defaults = dialog.slice(
    dialog.indexOf('function createDefaultUIState'),
    dialog.indexOf('function initFromKbInfo'),
  )
  const restoration = dialog.slice(
    dialog.indexOf('function applyOverridesToState'),
    dialog.indexOf('function initializeIngestionAdvisorMode'),
  )

  assert.match(defaults, /allowSmartWebAccess: false/)
  assert.match(defaults, /allowSmartReadOnlyMcp: false/)
  assert.match(restoration, /allow_web_access === true/)
  assert.match(restoration, /allow_read_only_mcp === true/)
  assert.match(dialog, /uploadConfirm\.smartWebAccessDescription/)
  assert.match(dialog, /uploadConfirm\.smartMcpAccessDescription/)
  assert.match(dialog, /aria-labelledby="smart-web-access-label"/)
  assert.match(dialog, /aria-labelledby="smart-mcp-access-label"/)
})

test('carries explicit provenance for reparses and isolates URL payloads in mixed batches', () => {
  assert.match(knowledgeBase, /knowledgeType = detail\.data\.type \|\| knowledgeType/)
  assert.match(knowledgeBase, /hasOwnProperty\.call\(detail\.data\.metadata \|\| \{\}, 'datasource_id'\)/)
  assert.match(knowledgeBase, /knowledgeType,\s*isDatasource,/s)

  const urlIsolation = knowledgeBase.slice(
    knowledgeBase.indexOf('const withoutIngestionAdvisor'),
    knowledgeBase.indexOf('const openUploadConfirmDialog'),
  )
  assert.match(urlIsolation, /const urlProcessConfig = \{ \.\.\.processConfig \}/)
  assert.match(urlIsolation, /delete urlProcessConfig\.ingestion_advisor/)
  assert.match(urlIsolation, /executeUploadBatch\(files, \{ processConfig, tagIds \}\)/)
  assert.match(urlIsolation, /executeUrlImport\(url, urlProcessConfig, tagIds\)/)
})

test('locks only advisor-owned chunking controls in smart mode', () => {
  assert.match(dialog, /v-model="uiState\.ingestionAdvisorMode"/)
  assert.match(dialog, /value="smart"/)
  assert.match(dialog, /value="off"/)
  assert.equal((dialog.match(/:disabled="isSmartAnalysis"/g) || []).length, 7)

  const nonChunkingSection = dialog.slice(
    dialog.indexOf('data-section="multimodal"'),
    dialog.indexOf('data-section="question"'),
  )
  assert.doesNotMatch(nonChunkingSection, /:disabled="isSmartAnalysis"/)
})

test('switching to knowledge base configuration sends explicit off mode', () => {
  assert.match(dialog, /<t-radio-button value="off">/)
  assert.match(dialog, /overrides\.ingestion_advisor = \{\s*mode: state\.ingestionAdvisorMode,/s)
})
