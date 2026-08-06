import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const component = readFileSync(new URL('./SettingDrawer.vue', import.meta.url), 'utf8')

test('caps the drawer width at the viewport width', () => {
  assert.match(component, /return `min\(\$\{preferredWidth\}, 100vw\)`/)
})

test('hides the resize handle when the viewport is narrower than its minimum width', () => {
  assert.match(component, /@media \(max-width: 479px\)[\s\S]*\.setting-drawer-resize-handle\s*\{\s*display: none;/)
})
