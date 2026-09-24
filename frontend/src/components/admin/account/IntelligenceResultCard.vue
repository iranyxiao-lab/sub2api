<template>
  <article ref="container" class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800" :data-run-id="run.id">
    <header class="flex flex-wrap items-start justify-between gap-2 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
      <div class="min-w-0">
        <h4 class="break-words font-medium text-gray-900 dark:text-gray-100">{{ run.question_snapshot?.title || t('admin.intelligence.title') }}</h4>
        <p class="mt-1 break-all text-xs text-gray-500">{{ run.model_snapshot }} · {{ formatDateTime(run.queued_at || run.created_at) }} · {{ t(`admin.intelligence.trigger_${run.trigger_type || 'legacy'}`) }}</p>
      </div>
      <div class="flex items-center gap-2 text-xs">
        <span class="rounded-full px-2.5 py-1 font-medium" :class="statusClass">{{ t(`admin.intelligence.state_${run.status}`) }}</span>
        <span class="tabular-nums text-gray-500">{{ elapsed }}s</span>
      </div>
    </header>
    <div v-if="active" class="flex items-center gap-3 px-4 py-6 text-sm text-gray-500" role="status">
      <LoadingSpinner size="sm" /> {{ t('admin.intelligence.backgroundHint') }}
    </div>
    <div v-if="run.error_message" class="m-4 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">
      <p class="font-medium">{{ errorLabel }}</p>
      <details class="mt-1 text-xs"><summary class="cursor-pointer">{{ t('admin.intelligence.technicalDetails') }}</summary><p class="mt-2 whitespace-pre-wrap break-words">{{ run.error_message }}</p></details>
    </div>
    <p v-if="run.response_text && run.status !== 'success' && !active" class="px-4 pt-2 text-xs text-amber-700 dark:text-amber-400">{{ t('admin.intelligence.partialOutput') }}</p>
    <p v-if="run.output_truncated" class="px-4 pt-2 text-xs text-amber-700 dark:text-amber-400">{{ t('admin.intelligence.truncatedOutput') }}</p>
    <template v-if="run.response_text">
      <div class="flex flex-wrap items-center justify-between gap-2 px-4 py-2">
        <div class="flex gap-1 text-xs">
          <button v-if="isHtml" class="rounded-lg px-3 py-1.5" :class="!source ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-500'" :aria-pressed="!source" @click="source = false">{{ t('admin.intelligence.preview') }}</button>
          <button v-if="isHtml" class="rounded-lg px-3 py-1.5" :class="source ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-500'" :aria-pressed="source" @click="source = true">{{ t('admin.intelligence.source') }}</button>
          <span v-else class="py-1.5 text-gray-500">{{ t('admin.intelligence.output') }}</span>
        </div>
        <div class="flex gap-3 text-xs text-primary-600 dark:text-primary-400">
          <button @click="copyOutput">{{ t('admin.intelligence.copyOutput') }}</button>
          <button @click="expanded = true">{{ t('admin.intelligence.expand') }}</button>
        </div>
      </div>
      <iframe v-if="isHtml && !source && visible" :srcdoc="html" sandbox="" referrerpolicy="no-referrer" class="h-60 w-full border-0 bg-white md:h-80" :title="t('admin.intelligence.preview') + ': ' + run.question_snapshot?.title" />
      <div v-else-if="isHtml && !source" class="h-60 animate-pulse bg-gray-50 md:h-80 dark:bg-dark-900" />
      <pre v-else class="max-h-60 overflow-auto whitespace-pre-wrap break-words bg-gray-50 p-4 text-sm text-gray-800 md:max-h-80 dark:bg-dark-900 dark:text-gray-200">{{ run.response_text }}</pre>
    </template>
    <p v-else-if="!active && !run.error_message" class="p-4 text-sm text-gray-500">{{ t('admin.intelligence.emptyOutput') }}</p>
    <footer class="flex flex-wrap items-start justify-between gap-3 border-t border-gray-100 px-4 py-3 dark:border-dark-700">
      <details class="min-w-0 flex-1 text-xs" @toggle="loadPrompt">
        <summary class="cursor-pointer text-gray-500">{{ t('admin.intelligence.viewPrompt') }}</summary>
        <p v-if="promptLoading" class="mt-2">{{ t('common.loading') }}</p>
        <button v-else-if="promptError" class="mt-2 text-red-600" @click="fetchPrompt">{{ t('admin.intelligence.refresh') }}</button>
        <pre v-else class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words text-gray-600 dark:text-gray-300">{{ prompt }}</pre>
      </details>
      <button v-if="['failed', 'interrupted'].includes(run.status)" :disabled="disableRun" class="text-xs text-primary-600 disabled:opacity-50 dark:text-primary-400" @click="emit('retry')">{{ t('admin.intelligence.retryRun') }}</button>
    </footer>
    <BaseDialog :show="expanded" :title="run.question_snapshot?.title || t('admin.intelligence.output')" width="full" :z-index="60" @close="expanded = false">
      <iframe v-if="isHtml && !source && expanded" :srcdoc="html" sandbox="" referrerpolicy="no-referrer" class="h-[70vh] w-full border-0 bg-white" :title="t('admin.intelligence.preview')" />
      <pre v-else class="max-h-[70vh] overflow-auto whitespace-pre-wrap break-words text-sm">{{ run.response_text }}</pre>
    </BaseDialog>
  </article>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { buildStaticHtmlPreview, extractIntelligenceHtml } from '@/utils/intelligencePreview'
import type { IntelligenceRun } from '@/types'

const props = defineProps<{ run: IntelligenceRun; now: number; disableRun: boolean }>()
const emit = defineEmits<{ retry: []; 'preview-open': [open: boolean] }>()
const { t } = useI18n()
const app = useAppStore()
const container = ref<HTMLElement | null>(null)
const visible = ref(false)
const source = ref(false)
const expanded = ref(false)
watch(expanded, open => emit('preview-open', open), { flush: 'sync' })
const prompt = ref('')
const promptLoaded = ref(false)
const promptLoading = ref(false)
const promptError = ref(false)
let observer: IntersectionObserver | undefined
const isHtml = computed(() => extractIntelligenceHtml(props.run.response_text) !== null)
const html = computed(() => buildStaticHtmlPreview(props.run.response_text))
const active = computed(() => ['queued', 'running'].includes(props.run.status))
const elapsed = computed(() => (active.value ? Math.max(0, props.now - Date.parse(props.run.started_at || props.run.queued_at)) / 1000 : props.run.latency_ms / 1000).toFixed(1))
const statusClass = computed(() => active.value ? 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300' : props.run.status === 'success' ? 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-300' : 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300')
const errorLabel = computed(() => t(`admin.intelligence.error_${['execution_timeout', 'queue_timeout', 'worker_interrupted'].includes(props.run.error_code || '') ? props.run.error_code : 'upstream_error'}`))

onMounted(() => {
  if (typeof IntersectionObserver === 'undefined') { visible.value = true; return }
  observer = new IntersectionObserver(entries => { if (entries.some(entry => entry.isIntersecting)) { visible.value = true; observer?.disconnect() } }, { rootMargin: '200px' })
  if (container.value) observer.observe(container.value)
})
onUnmounted(() => { observer?.disconnect(); if (expanded.value) emit('preview-open', false) })
const fetchPrompt = async () => {
  if (promptLoading.value) return
  promptLoading.value = true
  promptError.value = false
  try { prompt.value = (await adminAPI.scheduledTests.getRun(props.run.plan_id, props.run.id)).prompt_snapshot || ''; promptLoaded.value = true }
  catch { promptError.value = true }
  finally { promptLoading.value = false }
}
const loadPrompt = (event: Event) => { if ((event.target as HTMLDetailsElement).open && !promptLoaded.value) void fetchPrompt() }
const copyOutput = async () => {
  try { await navigator.clipboard.writeText(props.run.response_text); app.showSuccess(t('admin.intelligence.copied')) }
  catch { app.showError(t('admin.intelligence.copyFailed')) }
}
</script>
