import assert from 'node:assert/strict'
import test from 'node:test'

import { INTEGRATION_TAB_MIN_ROLE } from './integrations'

test('personal API integration is visible to every workspace member', () => {
  assert.equal(INTEGRATION_TAB_MIN_ROLE.api, 'viewer')
})
