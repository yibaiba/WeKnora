import assert from 'node:assert/strict'
import test from 'node:test'

import { asIngestionAnalysis } from './ingestionAnalysis'

const chunking = {
  strategy: 'auto',
  chunk_size: 512,
  chunk_overlap: 32,
  enable_parent_child: false,
  parent_chunk_size: 1024,
  child_chunk_size: 256,
  separators: ['\n\n'],
}

const baseCandidate = {
  id: 'candidate-1',
  config: chunking,
  chunk_count: 4,
  parent_chunk_count: 0,
  lengths: { minimum: 120, maximum: 500, average: 320, p50: 300, p95: 480 },
  structure: {
    present_types: ['section'],
    heading_retention: 1,
    faq_retention: 1,
    table_retention: 1,
  },
  diagnostics: { selected_tier: 'semantic', tier_chain: ['semantic'], rejected: [] },
  hard_valid: true,
  violations: [],
}

function analysisWithCandidate(candidate: Record<string, unknown>): Record<string, unknown> {
  return {
    document_kind: 'sectioned_text',
    confidence: 0.9,
    recommended_content_mode: 'text',
    reason_codes: ['section_structure'],
    summary: 'Structured document',
    recommended_chunking: chunking,
    applied_chunking: chunking,
    model_id: 'test-model',
    candidates: [candidate],
  }
}

test('normalizes historical smart analysis and legacy candidate scores', () => {
  const analysis = asIngestionAnalysis(analysisWithCandidate({
    ...baseCandidate,
    score: {
      structure_integrity: 0.9,
      chunk_size_balance: 0.8,
      boundary_quality: 0.7,
      overlap_efficiency: 0.6,
      parent_child: 1,
      total: 0.82,
    },
  }))

  assert.ok(analysis)
  assert.equal(analysis.applied_mode, 'smart')
  assert.deepEqual(analysis.fallback_reason_codes, [])
  assert.equal(analysis.candidates[0].score.semantic_integrity, 0.9)
  assert.equal(analysis.candidates[0].score.size_fit, 0.8)
  assert.equal(analysis.candidates[0].score.context_efficiency, 0.6)
  assert.deepEqual(analysis.candidates[0].structure_quality, {
    orphan_table_rows: 0,
    headerless_continuations: 0,
    split_atomic_blocks: 0,
    mixed_sections: 0,
    oversize_atomic_blocks: 0,
  })
  assert.deepEqual(analysis.candidates[0].block_descriptions, [])
})

test('preserves fallback mode, reasons, and semantic candidate quality', () => {
  const analysis = asIngestionAnalysis({
    ...analysisWithCandidate({
      ...baseCandidate,
      hard_valid: false,
      violations: ['table_header_missing'],
      score: {
        semantic_integrity: 0.4,
        boundary_quality: 0.6,
        size_fit: 0.8,
        context_efficiency: 0.9,
        parent_child: 1,
        total: 0.59,
      },
      structure_quality: {
        orphan_table_rows: 2,
        headerless_continuations: 1,
        split_atomic_blocks: 0,
        mixed_sections: 3,
        oversize_atomic_blocks: 0,
      },
    }),
    applied_mode: 'fallback',
    fallback_reason_codes: ['all_candidates_structurally_invalid'],
  })

  assert.ok(analysis)
  assert.equal(analysis.applied_mode, 'fallback')
  assert.deepEqual(analysis.fallback_reason_codes, ['all_candidates_structurally_invalid'])
  assert.equal(analysis.candidates[0].hard_valid, false)
  assert.equal(analysis.candidates[0].structure_quality.orphan_table_rows, 2)
  assert.equal(analysis.candidates[0].structure_quality.mixed_sections, 3)
})
