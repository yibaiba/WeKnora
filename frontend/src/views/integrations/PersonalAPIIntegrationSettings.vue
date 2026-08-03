<template>
  <div class="personal-api">
    <div v-if="loading" class="state-row">
      <t-loading size="small" />
      <span>{{ t('integrations.api.loading') }}</span>
    </div>
    <t-alert v-else-if="error" theme="error" :message="error">
      <template #operation>
        <t-button size="small" @click="load">{{ t('integrations.api.retry') }}</t-button>
      </template>
    </t-alert>
    <template v-else>
      <section class="panel endpoint-panel">
        <div>
          <h3>{{ t('integrations.api.personalTitle') }}</h3>
          <p>{{ t('integrations.api.personalSubtitle') }}</p>
        </div>
        <div class="copy-field">
          <t-input :model-value="apiBaseUrl" readonly class="mono" />
          <t-button variant="outline" :aria-label="t('integrations.api.copy')" @click="copy(apiBaseUrl)">
            <template #icon><t-icon name="file-copy" /></template>
            {{ t('integrations.api.copy') }}
          </t-button>
        </div>
      </section>

      <section class="panel">
        <div class="section-heading">
          <div>
            <h3>{{ t('integrations.api.myApiKeys') }}</h3>
            <p>{{ t('integrations.api.personalKeysHint') }}</p>
          </div>
          <t-button theme="primary" @click="createVisible = true">
            <template #icon><t-icon name="add" /></template>
            {{ t('integrations.api.createApiKey') }}
          </t-button>
        </div>
        <div v-if="keys.length === 0" class="empty-state">{{ t('integrations.api.noApiKeys') }}</div>
        <div v-else class="card-list">
          <article v-for="key in keys" :key="key.id" class="key-card">
            <div class="key-card__main">
              <div class="key-name-row">
                <strong>{{ key.name }}</strong>
                <span class="permission-badge">{{ t('integrations.api.personalFixedCapabilities') }}</span>
              </div>
              <code>{{ key.api_key }}</code>
              <p>{{ knowledgeScopeLabel(key.knowledge_base_ids) }}</p>
            </div>
            <div class="key-card__actions">
              <t-button variant="text" :aria-label="t('integrations.api.copy')" @click="copy(key.api_key)">
                <t-icon name="file-copy" />
              </t-button>
              <t-button
                variant="text"
                theme="danger"
                :aria-label="t('integrations.api.deleteApiKey')"
                @click="removeKey(key.id)"
              >
                <t-icon name="delete" />
              </t-button>
            </div>
          </article>
        </div>
      </section>

      <section class="panel">
        <div class="section-heading">
          <div>
            <h3>{{ t('integrations.api.personalExamples') }}</h3>
            <p>{{ t('integrations.api.personalExamplesHint') }}</p>
          </div>
        </div>
        <t-tabs v-model="exampleTab">
          <t-tab-panel value="retrieve" :label="t('integrations.api.capabilityRetrieve')">
            <pre><code>{{ retrieveExample }}</code></pre>
          </t-tab-panel>
          <t-tab-panel value="chat" :label="t('integrations.api.capabilityChat')">
            <pre><code>{{ chatExample }}</code></pre>
          </t-tab-panel>
        </t-tabs>
      </section>

      <section class="panel">
        <div class="section-heading history-heading">
          <div>
            <h3>{{ t('integrations.api.personalHistory') }}</h3>
            <p>{{ t('integrations.api.personalHistoryHint') }}</p>
          </div>
          <t-input
            v-model="historyKeyword"
            clearable
            :placeholder="t('integrations.api.personalHistorySearch')"
            @enter="searchSessions"
            @clear="searchSessions"
          >
            <template #suffix-icon><t-icon name="search" /></template>
          </t-input>
        </div>
        <div v-if="sessionsLoading" class="state-row"><t-loading size="small" /></div>
        <div v-else-if="sessions.length === 0" class="empty-state">
          {{ t('integrations.api.personalHistoryEmpty') }}
        </div>
        <div v-else class="history-layout">
          <div class="session-list">
            <button
              v-for="session in sessions"
              :key="session.id"
              type="button"
              class="session-item"
              :class="{ 'session-item--active': selectedSessionId === session.id }"
              @click="openSession(session.id)"
            >
              <span>{{ session.title || t('integrations.api.personalUntitledSession') }}</span>
              <small>{{ formatDate(session.updated_at || session.created_at) }}</small>
            </button>
          </div>
          <div class="message-list" aria-live="polite">
            <div v-if="messagesLoading" class="state-row"><t-loading size="small" /></div>
            <div v-else-if="!selectedSessionId" class="empty-state">
              {{ t('integrations.api.personalHistorySelect') }}
            </div>
            <div v-else-if="messages.length === 0" class="empty-state">
              {{ t('integrations.api.personalMessagesEmpty') }}
            </div>
            <template v-else>
              <article v-for="message in messages" :key="message.id" class="message-item">
                <strong>{{ message.role === 'user' ? t('integrations.api.personalRoleUser') : t('integrations.api.personalRoleAssistant') }}</strong>
                <p>{{ message.content }}</p>
              </article>
            </template>
          </div>
        </div>
        <t-pagination
          v-if="historyTotal > historyPageSize"
          v-model="historyPage"
          :page-size="historyPageSize"
          :total="historyTotal"
          size="small"
          class="history-pagination"
        />
      </section>
    </template>

    <t-dialog
      v-model:visible="createVisible"
      :header="t('integrations.api.createApiKey')"
      :confirm-btn="{ content: t('common.confirm'), loading: creating }"
      @confirm="createKey"
    >
      <div class="create-form">
        <label for="personal-api-key-name">{{ t('integrations.api.apiKeyName') }}</label>
        <t-input
          id="personal-api-key-name"
          v-model="form.name"
          :placeholder="t('integrations.api.apiKeyNamePlaceholder')"
        />
        <label>{{ t('integrations.api.apiKeyKnowledgeScope') }}</label>
        <t-select
          v-model="form.knowledgeBaseIds"
          multiple
          filterable
          :options="knowledgeBaseOptions"
          :placeholder="t('integrations.api.personalKnowledgeRequired')"
        />
        <p class="form-hint">{{ t('integrations.api.personalKnowledgeHint') }}</p>
      </div>
    </t-dialog>

    <t-dialog
      v-model:visible="tokenVisible"
      :header="t('integrations.api.personalTokenCreated')"
      :confirm-btn="{ content: t('integrations.api.copy'), theme: 'primary' }"
      :cancel-btn="null"
      :close-on-overlay-click="false"
      @confirm="copy(createdToken)"
    >
      <p>{{ t('integrations.api.personalTokenCreatedHint') }}</p>
      <t-textarea :value="createdToken" readonly autosize />
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { listKnowledgeBases } from '@/api/knowledge-base'
import {
  createPersonalAPIKey,
  deletePersonalAPIKey,
  listPersonalAPIKeys,
  listPersonalAPISessionMessages,
  listPersonalAPISessions,
  type PersonalAPIMessage,
  type PersonalAPISession,
  type TenantAPIKey,
} from '@/api/tenant'
import { useAuthStore } from '@/stores/auth'
import { getApiBaseUrl } from '@/utils/api-base'

const { t } = useI18n()
const authStore = useAuthStore()
const loading = ref(true)
const error = ref('')
const keys = ref<TenantAPIKey[]>([])
const knowledgeBases = ref<Array<{ id: string; name: string }>>([])
const sessions = ref<PersonalAPISession[]>([])
const messages = ref<PersonalAPIMessage[]>([])
const sessionsLoading = ref(false)
const messagesLoading = ref(false)
const selectedSessionId = ref('')
const historyKeyword = ref('')
const historyPage = ref(1)
const historyPageSize = 20
const historyTotal = ref(0)
const createVisible = ref(false)
const tokenVisible = ref(false)
const creating = ref(false)
const createdToken = ref('')
const exampleTab = ref<'retrieve' | 'chat'>('retrieve')
const form = reactive({ name: '', knowledgeBaseIds: [] as string[] })

const tenantId = computed(() => Number(authStore.currentTenantId || 0))
const apiBaseUrl = computed(() => {
  const configured = getApiBaseUrl().trim().replace(/\/$/, '')
  const origin = typeof window !== 'undefined' && window.location.origin !== 'null' ? window.location.origin : ''
  return `${configured || origin}/api/v1`
})
const knowledgeBaseOptions = computed(() => knowledgeBases.value.map((item) => ({
  label: item.name || item.id,
  value: item.id,
})))
const exampleKey = computed(() => keys.value[0]?.api_key || '<YOUR_API_KEY>')
const exampleKnowledgeBase = computed(() => keys.value[0]?.knowledge_base_ids?.[0] || '<KNOWLEDGE_BASE_ID>')
const retrieveExample = computed(() => `curl -X POST ${apiBaseUrl.value}/knowledge-search \\\n  -H "X-API-Key: ${exampleKey.value}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"query":"<QUESTION>","knowledge_base_ids":["${exampleKnowledgeBase.value}"]}'`)
const chatExample = computed(() => `# 1. Create an isolated personal API session
curl -X POST ${apiBaseUrl.value}/sessions -H "X-API-Key: ${exampleKey.value}" -H "Content-Type: application/json" -d '{}'

# 2. Ask with the returned session id
curl -N -X POST ${apiBaseUrl.value}/knowledge-chat/<SESSION_ID> \\\n  -H "X-API-Key: ${exampleKey.value}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"query":"<QUESTION>","knowledge_base_ids":["${exampleKnowledgeBase.value}"]}'`)

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (!tenantId.value) throw new Error(t('integrations.api.loadFailed'))
    await Promise.all([loadKeys(), loadKnowledgeBases(), loadSessions()])
  } catch (err: any) {
    error.value = err?.message || t('integrations.api.loadFailed')
  } finally {
    loading.value = false
  }
}

async function loadKeys() {
  const response = await listPersonalAPIKeys(tenantId.value)
  if (!response.success) throw new Error(response.message || t('integrations.api.loadApiKeysFailed'))
  keys.value = response.data || []
}

async function loadKnowledgeBases() {
  const response: any = await listKnowledgeBases({ creator: 'all' })
  const rows = Array.isArray(response?.data) ? response.data : []
  knowledgeBases.value = rows.map((item: any) => ({ id: String(item.id), name: item.name || item.id }))
}

async function loadSessions() {
  if (!tenantId.value) return
  sessionsLoading.value = true
  try {
    const response = await listPersonalAPISessions(tenantId.value, {
      page: historyPage.value,
      page_size: historyPageSize,
      keyword: historyKeyword.value,
    })
    sessions.value = response.data || []
    historyTotal.value = response.total || 0
    if (selectedSessionId.value && !sessions.value.some((item) => item.id === selectedSessionId.value)) {
      selectedSessionId.value = ''
      messages.value = []
    }
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.api.personalHistoryLoadFailed'))
  } finally {
    sessionsLoading.value = false
  }
}

function searchSessions() {
  if (historyPage.value !== 1) {
    historyPage.value = 1
    return
  }
  void loadSessions()
}

async function openSession(sessionId: string) {
  selectedSessionId.value = sessionId
  messagesLoading.value = true
  try {
    const response = await listPersonalAPISessionMessages(tenantId.value, sessionId)
    messages.value = response.data || []
  } catch (err: any) {
    messages.value = []
    MessagePlugin.error(err?.message || t('integrations.api.personalHistoryLoadFailed'))
  } finally {
    messagesLoading.value = false
  }
}

async function createKey() {
  if (!form.name.trim() || form.knowledgeBaseIds.length === 0) {
    MessagePlugin.error(t('integrations.api.personalCreateRequired'))
    return
  }
  creating.value = true
  try {
    const response = await createPersonalAPIKey(tenantId.value, {
      name: form.name.trim(),
      knowledge_base_ids: [...form.knowledgeBaseIds],
    })
    const token = response.data?.token || response.data?.api_key
    if (!response.success || !token) throw new Error(response.message || t('integrations.api.createApiKeyFailed'))
    createdToken.value = token
    createVisible.value = false
    tokenVisible.value = true
    form.name = ''
    form.knowledgeBaseIds = []
    await loadKeys()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.api.createApiKeyFailed'))
  } finally {
    creating.value = false
  }
}

function removeKey(keyId: number) {
  const dialog = DialogPlugin.confirm({
    header: t('integrations.api.deleteApiKey'),
    body: t('integrations.api.deleteApiKeyConfirm'),
    confirmBtn: { content: t('integrations.api.deleteApiKey'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      const response = await deletePersonalAPIKey(tenantId.value, keyId)
      if (!response.success) MessagePlugin.error(response.message || t('integrations.api.deleteApiKeyFailed'))
      else await loadKeys()
      dialog.destroy()
    },
    onClose: () => dialog.destroy(),
  })
}

function knowledgeScopeLabel(ids: string[]) {
  const names = ids.map((id) => knowledgeBases.value.find((item) => item.id === id)?.name || id)
  return `${t('integrations.api.apiKeyKnowledgeScope')}: ${names.join(', ')}`
}

async function copy(value: string) {
  try {
    await navigator.clipboard.writeText(value)
    MessagePlugin.success(t('integrations.api.copySuccess'))
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('common.copyFailed'))
  }
}

function formatDate(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString()
}

watch(tenantId, (next, previous) => {
  if (next && previous && next !== previous) void load()
})
watch(historyPage, () => void loadSessions())

onMounted(load)
</script>

<style scoped lang="less">
.personal-api { display: grid; gap: 16px; width: 100%; }
.panel { padding: 20px; border: 1px solid var(--td-component-stroke); border-radius: 10px; background: var(--td-bg-color-container); }
.panel h3 { margin: 0 0 6px; color: var(--td-text-color-primary); font-size: 16px; }
.panel p { margin: 0; color: var(--td-text-color-secondary); line-height: 1.6; }
.endpoint-panel { display: grid; grid-template-columns: minmax(220px, 1fr) minmax(320px, 1.2fr); align-items: center; gap: 24px; }
.copy-field, .section-heading, .key-name-row, .key-card, .key-card__actions { display: flex; align-items: center; gap: 8px; }
.copy-field .t-input { min-width: 0; }
.mono, code, pre { font-family: var(--app-font-family-mono); }
.section-heading { justify-content: space-between; margin-bottom: 16px; }
.history-heading :deep(.t-input) { width: min(320px, 100%); }
.card-list { display: grid; gap: 10px; }
.key-card { justify-content: space-between; padding: 14px 16px; border: 1px solid var(--td-component-stroke); border-radius: 8px; }
.key-card__main { min-width: 0; }
.key-card code { display: block; margin: 8px 0; overflow: hidden; color: var(--td-text-color-primary); text-overflow: ellipsis; white-space: nowrap; }
.permission-badge { padding: 2px 8px; border-radius: 999px; background: var(--td-success-color-light); color: var(--td-success-color); font-size: 12px; }
.empty-state, .state-row { display: flex; min-height: 96px; align-items: center; justify-content: center; color: var(--td-text-color-secondary); }
pre { margin: 12px 0 0; padding: 14px; overflow: auto; border-radius: 8px; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-primary); line-height: 1.6; white-space: pre-wrap; }
.history-layout { display: grid; grid-template-columns: minmax(220px, 0.8fr) minmax(300px, 1.4fr); gap: 16px; }
.session-list { display: grid; align-content: start; gap: 6px; }
.session-item { display: grid; gap: 4px; min-height: 52px; padding: 10px 12px; border: 1px solid transparent; border-radius: 8px; background: transparent; color: var(--td-text-color-primary); text-align: left; cursor: pointer; }
.session-item:hover, .session-item:focus-visible, .session-item--active { border-color: var(--td-brand-color); background: var(--td-brand-color-light); outline: none; }
.session-item small { color: var(--td-text-color-secondary); }
.message-list { min-height: 180px; max-height: 440px; padding: 12px; overflow: auto; border: 1px solid var(--td-component-stroke); border-radius: 8px; }
.message-item { padding: 10px 0; border-bottom: 1px solid var(--td-component-stroke); }
.message-item:last-child { border-bottom: 0; }
.message-item p { margin-top: 4px; white-space: pre-wrap; word-break: break-word; }
.history-pagination { margin-top: 16px; }
.create-form { display: grid; gap: 10px; }
.create-form label { color: var(--td-text-color-primary); font-weight: 600; }
.form-hint { font-size: 13px; }
@media (max-width: 780px) {
  .endpoint-panel, .history-layout { grid-template-columns: 1fr; }
  .section-heading { align-items: stretch; flex-direction: column; }
  .copy-field { align-items: stretch; flex-direction: column; }
  .key-card { align-items: flex-start; }
}
</style>
