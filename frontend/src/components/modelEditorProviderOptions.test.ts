import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const component = readFileSync(new URL('./ModelEditorDialog.vue', import.meta.url), 'utf8')

const localeSources = ['zh-CN', 'en-US', 'ru-RU', 'ko-KR'].map(locale => ({
  locale,
  source: readFileSync(new URL(`../i18n/locales/${locale}.ts`, import.meta.url), 'utf8'),
}))

test('offers Ollama Cloud as a remote Chat and VLLM provider fallback', () => {
  assert.match(component, /value: 'ollama_cloud'/)
  assert.match(component, /chat: 'https:\/\/ollama\.com\/v1'/)
  assert.match(component, /vllm: 'https:\/\/ollama\.com\/v1'/)
  assert.match(component, /modelTypes: \['chat', 'vllm'\]/)
})

test('keeps local Ollama separate from the Ollama Cloud provider', () => {
  assert.match(component, /formData\.source === 'remote'/)
  assert.match(component, /formData\.source === 'local'/)
  assert.doesNotMatch(component, /value: 'ollama_cloud'[\s\S]{0,300}modelTypes: \[[^\]]*'embedding'/)
})

test('preserves the selected provider in connection checks and saves', () => {
  assert.match(component, /provider: formData\.value\.provider/)
  assert.match(component, /emit\('confirm', \{\s*\.\.\.formData\.value,/)
})

test('defines Ollama Cloud labels in every supported locale', () => {
  for (const { locale, source } of localeSources) {
    assert.match(source, /ollama_cloud:\s*\{/, `${locale} is missing ollama_cloud`)
    assert.match(source, /ollama_cloud:\s*\{\s*label: 'Ollama Cloud'/, `${locale} is missing the label`)
  }
})

test('keeps the Provider popup inside narrow viewports', () => {
  assert.match(component, /@media \(max-width: 479px\)[\s\S]*\.provider-select-popup \.t-popup__content/)
  assert.match(component, /width: calc\(100vw - 12px\) !important/)
  assert.match(component, /\.provider-select-popup \.provider-option \.provider-desc[\s\S]*white-space: normal/)
})
