<template>
  <BaseDialog :show="show" :title="accountId ? t('admin.intelligence.title') : t('admin.intelligence.bank')" width="extra-wide" :close-on-escape="!previewOpen && !confirmDeletePlan && deletingQuestion === null" @close="emit('close')">
    <div class="space-y-4 text-sm text-gray-800 dark:text-gray-200">
      <div v-if="accountId" class="flex flex-wrap items-center justify-between gap-3">
        <div class="min-w-0">
          <p class="truncate font-medium">{{ accountName || '#' + accountId }}</p>
          <p class="mt-1 text-xs text-gray-500">{{ plan?.model_id || t('admin.intelligence.notConfigured') }} · {{ plan?.enabled ? t('admin.intelligence.scheduleOn') : t('admin.intelligence.scheduleOff') }}</p>
        </div>
        <button class="btn btn-primary" :disabled="loading || busy || activeCount > 0" @click="runNow">
          <Icon name="play" size="sm" /> {{ busy ? t('admin.intelligence.submitting') : activeCount ? t('admin.intelligence.state_running') : t('admin.intelligence.runNow') }}
        </button>
      </div>
      <nav v-if="accountId" class="flex gap-1 border-b border-gray-200 pb-2 dark:border-dark-600" :aria-label="t('admin.intelligence.title')">
        <button v-for="item in tabs" :key="item.value" class="rounded-lg px-4 py-2 font-medium transition-colors" :class="tab === item.value ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-700'" :aria-current="tab === item.value ? 'page' : undefined" @click="tab = item.value">{{ item.label }}</button>
      </nav>
      <p v-if="loadError" class="rounded-lg bg-red-50 p-3 text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ t('admin.intelligence.loadFailed') }} <button class="ml-2 underline" @click="loadAccount">{{ t('admin.intelligence.refresh') }}</button></p>
      <div v-if="accountId && tab === 'history'" class="space-y-4">
        <div class="flex items-center justify-between gap-3">
          <Select v-model="status" class="w-40" :options="statusOptions" :aria-label="t('admin.intelligence.filterStatus')" />
          <button class="btn btn-secondary" :disabled="historyLoading" @click="refreshHistory">{{ t('admin.intelligence.refresh') }}</button>
        </div>
        <p v-if="historyError" class="rounded-lg bg-red-50 p-3 text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ t('admin.intelligence.historyLoadFailed') }}</p>
        <div v-if="loading || (historyLoading && !historyLoaded)" class="space-y-3" role="status">
          <div v-for="n in 2" :key="n" class="h-40 animate-pulse rounded-xl bg-gray-100 dark:bg-dark-800" />
          <p class="text-center text-gray-500">{{ t('common.loading') }}</p>
        </div>
        <template v-else-if="!loadError">
          <EmptyState v-if="!plan" :title="t('admin.intelligence.notConfigured')" :description="t('admin.intelligence.setupHint')" :action-text="t('admin.intelligence.configure')" @action="tab = 'settings'" />
          <EmptyState v-else-if="historyLoaded && !results.length && !historyError" :title="t(status ? 'admin.intelligence.noMatches' : 'admin.intelligence.noRuns')" :description="t('admin.intelligence.firstRunHint')">
            <template #action><button class="btn btn-primary" :disabled="busy || activeCount > 0" @click="runNow">{{ t('admin.intelligence.runNow') }}</button></template>
          </EmptyState>
          <IntelligenceResultCard v-for="result in results" :key="result.id" :run="result" :now="now" :disable-run="busy || activeCount > 0" @retry="runNow" @preview-open="previewOpen = $event" />
          <Pagination v-if="total > 10" :total="total" :page="page" :page-size="10" :page-size-options="[10]" @update:page="page = $event" />
        </template>
      </div>
      <div v-if="accountId && tab === 'settings'" class="space-y-4">
        <fieldset :disabled="busy" class="grid gap-4 rounded-xl border border-gray-200 p-4 sm:grid-cols-2 dark:border-dark-600">
          <div><label class="input-label mb-1 block">{{ t('admin.scheduledTests.model') }}</label><Select v-model="form.model_id" :options="availableModels" searchable :placeholder="t('admin.intelligence.selectModel')" /><p v-if="!modelOptions.length" class="mt-1 text-xs text-gray-500">{{ t('admin.intelligence.modelsUnavailable') }}</p></div>
          <div class="flex items-center justify-between gap-2"><label>{{ t('admin.intelligence.scheduleOn') }}</label><Toggle v-model="form.enabled" /></div>
          <label class="sm:col-span-2">{{ t('admin.intelligence.customPrompt') }}<textarea v-model="form.custom_prompt" maxlength="4000" rows="3" class="input mt-1 w-full" /></label>
          <fieldset class="sm:col-span-2"><legend class="mb-2 font-medium">{{ t('admin.intelligence.selectQuestions') }}</legend>
            <p v-if="questionsError" class="text-red-600">{{ t('admin.intelligence.bankLoadFailed') }} <button class="underline" @click="loadQuestions">{{ t('admin.intelligence.refresh') }}</button></p>
            <div class="grid max-h-52 gap-2 overflow-y-auto sm:grid-cols-2">
              <label v-for="question in questions" :key="question.id" class="flex items-start gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600"><input v-model="selectedQuestionId" type="radio" :name="`intelligence-question-${accountId}`" :value="question.id" class="mt-1" /><span>{{ question.title }}</span></label>
            </div>
            <p class="mt-2 text-xs text-gray-500">{{ t('admin.intelligence.currentSelectionHint') }}</p>
            <p v-if="form.question_ids.length > 1" class="mt-2 text-amber-600" role="alert">{{ t('admin.intelligence.chooseSingleQuestion') }}</p>
            <button class="mt-2 text-primary-600" @click="tab = 'bank'">{{ t('admin.intelligence.manageBank') }}</button>
          </fieldset>
          <div v-if="form.enabled"><label class="input-label mb-1 block">{{ t('admin.intelligence.frequency') }}</label><Select :model-value="preset" :options="presets" @update:model-value="setPreset" /></div>
          <label v-if="form.enabled">{{ t('admin.scheduledTests.cronExpression') }}<input v-model="form.cron_expression" class="input mt-1 w-full" /><span class="mt-1 block text-xs text-gray-500">{{ t('admin.intelligence.scheduleHint') }}</span></label>
          <label>{{ t('admin.scheduledTests.maxResults') }}<input v-model.number="form.max_results" type="number" min="1" max="200" class="input mt-1 w-full" /></label>
          <p class="self-end text-xs text-gray-500">{{ t('admin.scheduledTests.nextRun') }}: {{ plan?.enabled && plan.next_run_at ? formatDateTime(plan.next_run_at) : '-' }}</p>
        </fieldset>
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-primary" :disabled="busy || !validForm" @click="savePlan(false)">{{ t('common.save') }}</button>
          <button class="btn btn-secondary" :disabled="busy || !validForm || activeCount > 0" @click="savePlan(true)">{{ t('admin.intelligence.saveAndRun') }}</button>
          <button v-if="plan" class="btn btn-secondary text-red-600" :disabled="busy || activeCount > 0" @click="confirmDeletePlan = true">{{ t('common.delete') }}</button>
        </div>
      </div>
      <div v-if="!accountId || tab === 'bank'" class="space-y-3">
        <div class="flex items-center justify-between"><p class="text-gray-500">{{ t('admin.intelligence.bankHint') }}</p><button class="btn btn-primary" @click="startNewQuestion"><Icon name="plus" size="sm" /> {{ t('admin.intelligence.addQuestion') }}</button></div>
        <p v-if="questionsError" class="text-red-600">{{ t('admin.intelligence.bankLoadFailed') }} <button class="underline" @click="loadQuestions">{{ t('admin.intelligence.refresh') }}</button></p>
        <div v-if="editingQuestion" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
          <label class="block">{{ t('admin.intelligence.questionTitle') }}<input v-model="editingQuestion.title" maxlength="160" class="input mt-1 w-full" /></label>
          <label class="block">{{ t('admin.intelligence.prompt') }}<textarea v-model="editingQuestion.prompt" maxlength="8000" rows="6" class="input mt-1 w-full" /></label>
          <div class="flex gap-2"><button class="btn btn-primary" :disabled="busy || !editingQuestion.title.trim() || !editingQuestion.prompt.trim()" @click="saveQuestion">{{ t('common.save') }}</button><button class="btn btn-secondary" @click="editingQuestion = null">{{ t('common.cancel') }}</button></div>
        </div>
        <div v-for="question in questions" :key="question.id" class="flex items-start justify-between gap-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
          <div class="min-w-0"><p class="font-medium">{{ question.title }}</p><p class="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-words text-gray-500">{{ question.prompt }}</p></div>
          <div v-if="!question.built_in" class="flex shrink-0 gap-1"><button :title="t('common.edit')" class="p-2" @click="editingQuestion = { ...question }"><Icon name="edit" size="sm" /></button><button :title="t('common.delete')" class="p-2 text-red-600" @click="deletingQuestion = question.id"><Icon name="trash" size="sm" /></button></div>
        </div>
      </div>
    </div>
  </BaseDialog>
  <ConfirmDialog :show="confirmDeletePlan || deletingQuestion !== null" :title="t('common.delete')" :message="t(confirmDeletePlan ? 'admin.scheduledTests.confirmDelete' : 'admin.intelligence.confirmDelete')" :confirm-text="t('common.delete')" :cancel-text="t('common.cancel')" danger @confirm="deleteConfirmed" @cancel="confirmDeletePlan = false; deletingQuestion = null" />
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import IntelligenceResultCard from './IntelligenceResultCard.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { createRequestId } from '@/utils/requestId'
import type { IntelligenceQuestion, IntelligenceRun, ScheduledTestPlan } from '@/types'

const props = defineProps<{ show: boolean; accountId: number | null; accountName?: string; modelOptions: SelectOption[] }>()
const emit = defineEmits<{ close: []; 'open-bank': [] }>()
const { t } = useI18n()
const app = useAppStore()
type Tab = 'history' | 'settings' | 'bank'
const tab = ref<Tab>('history')
const tabs = computed(() => [{ value: 'history' as Tab, label: t('admin.intelligence.history') }, { value: 'settings' as Tab, label: t('admin.intelligence.settings') }, { value: 'bank' as Tab, label: t('admin.intelligence.bank') }])
const questions = ref<IntelligenceQuestion[]>([])
const plan = ref<ScheduledTestPlan | null>(null)
const results = ref<IntelligenceRun[]>([])
const editingQuestion = ref<IntelligenceQuestion | null>(null)
const deletingQuestion = ref<number | null>(null)
const confirmDeletePlan = ref(false)
const previewOpen = ref(false)
const busy = ref(false)
const loading = ref(false)
const loadError = ref(false)
const questionsError = ref(false)
const historyLoading = ref(false)
const historyLoaded = ref(false)
const historyError = ref(false)
const page = ref(1)
const total = ref(0)
const status = ref('')
const activeCount = ref(0)
const now = ref(Date.now())
const defaults = () => ({ model_id: '', cron_expression: '0 9 * * *', question_ids: [] as number[], custom_prompt: '', max_results: 100, enabled: false })
const form = reactive(defaults())
const selectedQuestionId = computed({
  get: () => form.question_ids.length === 1 ? form.question_ids[0] : null,
  set: (id: number | null) => { form.question_ids = id === null ? [] : [id] }
})
const validForm = computed(() => Boolean(form.model_id && form.question_ids.length === 1 && form.max_results >= 1 && form.max_results <= 200))
const formChanged = computed(() => {
  const saved = plan.value
  return !saved || form.model_id !== saved.model_id || form.custom_prompt !== saved.custom_prompt ||
    form.enabled !== saved.enabled || form.cron_expression !== saved.cron_expression ||
    form.max_results !== saved.max_results || JSON.stringify(form.question_ids) !== JSON.stringify(saved.question_ids)
})
const availableModels = computed(() => form.model_id && !props.modelOptions.some(m => m.value === form.model_id) ? [{ value: form.model_id, label: form.model_id }, ...props.modelOptions] : props.modelOptions)
const statusOptions = computed(() => [{ value: '', label: t('admin.intelligence.allStatuses') }, ...['queued', 'running', 'success', 'failed', 'interrupted'].map(value => ({ value, label: t(`admin.intelligence.state_${value}`) }))])
const presets = computed(() => [{ value: '*/30 * * * *', label: t('admin.intelligence.every30Minutes') }, { value: '0 * * * *', label: t('admin.intelligence.hourly') }, { value: '0 9 * * *', label: t('admin.intelligence.daily') }, { value: 'custom', label: t('admin.intelligence.customSchedule') }])
const preset = computed(() => presets.value.some(p => p.value === form.cron_expression) ? form.cron_expression : 'custom')
const setPreset = (value: string | number | boolean | null) => { if (typeof value === 'string' && value !== 'custom') form.cron_expression = value }
let generation = 0
let historyVersion = 0
let accountController: AbortController | undefined
let historyController: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
const submissionKeys = new Map<number, string>()

const schedulePoll = () => {
  clearTimeout(timer)
  if (props.show && props.accountId && !document.hidden) timer = setTimeout(() => void (plan.value ? refreshHistory() : loadAccount()), activeCount.value ? 2000 : 15000)
}
const loadQuestions = async () => {
  const epoch = generation
  questionsError.value = false
  try { const data = await adminAPI.scheduledTests.listQuestions(); if (epoch === generation) questions.value = data }
  catch { if (epoch === generation) questionsError.value = true }
}
const refreshHistory = async () => {
  if (!plan.value || !props.show) return
  historyController?.abort()
  const controller = new AbortController()
  historyController = controller
  const version = ++historyVersion
  const epoch = generation
  const id = plan.value.id
  historyLoading.value = true
  try {
    const data = await adminAPI.scheduledTests.listRuns(id, page.value, status.value, controller.signal)
    if (epoch !== generation || version !== historyVersion) return
    results.value = data.items
    total.value = data.total
    activeCount.value = data.active_count
    historyLoaded.value = true
    historyError.value = false
  } catch { if (!controller.signal.aborted && epoch === generation && version === historyVersion) historyError.value = true }
  finally { if (epoch === generation && version === historyVersion) { historyLoading.value = false; schedulePoll() } }
}
const loadAccount = async () => {
  if (!props.accountId || !props.show) return
  accountController?.abort()
  const controller = new AbortController()
  accountController = controller
  const epoch = generation
  const accountId = props.accountId
  loading.value = true
  loadError.value = false
  try {
    const plans = await adminAPI.scheduledTests.listByAccount(accountId, controller.signal)
    if (epoch !== generation || controller.signal.aborted) return
    plan.value = plans.find(item => item.test_kind === 'intelligence') ?? null
    if (plan.value) {
      Object.assign(form, { model_id: plan.value.model_id, cron_expression: plan.value.cron_expression, question_ids: [...plan.value.question_ids], custom_prompt: plan.value.custom_prompt, max_results: plan.value.max_results, enabled: plan.value.enabled })
      await refreshHistory()
    }
  } catch { if (epoch === generation && !controller.signal.aborted) loadError.value = true }
  finally { if (epoch === generation && !controller.signal.aborted) { loading.value = false; schedulePoll() } }
}
watch(() => [props.show, props.accountId] as const, ([show], _, onCleanup) => {
  generation++
  accountController?.abort(); historyController?.abort(); clearTimeout(timer)
  plan.value = null; results.value = []; total.value = 0; activeCount.value = 0
  historyLoaded.value = false; historyError.value = false; historyLoading.value = false; loadError.value = false; loading.value = false
  page.value = 1; status.value = ''; tab.value = props.accountId ? 'history' : 'bank'
  editingQuestion.value = null; confirmDeletePlan.value = false; deletingQuestion.value = null
  Object.assign(form, defaults())
  if (show) { void loadQuestions(); void loadAccount() }
  onCleanup(() => { accountController?.abort(); historyController?.abort(); clearTimeout(timer) })
}, { immediate: true })
watch(status, () => { if (page.value !== 1) page.value = 1; else void refreshHistory() })
watch(page, () => void refreshHistory())
const visibilityChanged = () => { clearTimeout(timer); if (!document.hidden && props.show) void (plan.value ? refreshHistory() : loadAccount()) }
onMounted(() => { document.addEventListener('visibilitychange', visibilityChanged); clockTimer = setInterval(() => { if (props.show && !document.hidden) now.value = Date.now() }, 1000) })
onUnmounted(() => { generation++; accountController?.abort(); historyController?.abort(); clearTimeout(timer); clearInterval(clockTimer); document.removeEventListener('visibilitychange', visibilityChanged) })

const runNow = async () => {
  if (loading.value || busy.value || activeCount.value) return
  if (!validForm.value) { tab.value = 'settings'; return }
  if (formChanged.value) await savePlan(true)
  else await submitRun()
}
const submitRun = async () => {
  if (!plan.value || busy.value || activeCount.value) return
  const id = plan.value.id
  const epoch = generation
  let submitted = false
  busy.value = true
  try {
    const key = submissionKeys.get(id) || createRequestId()
    submissionKeys.set(id, key)
    submitted = true
    const run = await adminAPI.scheduledTests.createRun(id, key)
    submissionKeys.delete(id)
    if (epoch !== generation) return
    tab.value = 'history'; status.value = ''; page.value = 1
    results.value = [run, ...results.value.filter(item => item.id !== run.id)].slice(0, 10)
    historyLoaded.value = true; historyError.value = false
    activeCount.value = ['queued', 'running'].includes(run.status) ? 1 : 0
    schedulePoll()
  } catch {
    if (epoch === generation) {
      app.showError(t(submitted ? 'admin.intelligence.submitUnknown' : 'admin.intelligence.requestIdFailed'))
      if (submitted) void refreshHistory()
    }
  }
  finally { busy.value = false }
}
const savePlan = async (start: boolean) => {
  if (!props.accountId || loading.value || busy.value || !validForm.value || (start && activeCount.value)) return
  // Do not reuse an unresolved request for a different selection or silently run its old snapshot.
  if (plan.value && formChanged.value && submissionKeys.has(plan.value.id)) {
    app.showError(t('admin.intelligence.pendingSubmission'))
    void refreshHistory()
    return
  }
  const epoch = generation
  const accountId = props.accountId
  const currentPlan = plan.value
  busy.value = true
  try {
    const data = { ...form, question_ids: [...form.question_ids], account_id: accountId, test_kind: 'intelligence' as const, auto_recover: false }
    const saved = currentPlan ? await adminAPI.scheduledTests.update(currentPlan.id, data) : await adminAPI.scheduledTests.create(data)
    if (epoch !== generation) return
    plan.value = saved; app.showSuccess(t('admin.scheduledTests.updateSuccess'))
  } catch { if (epoch === generation) app.showError(t('admin.intelligence.saveFailed')); return }
  finally { busy.value = false }
  if (epoch !== generation) return
  // A subsequent submission failure must not be reported as a failed save.
  if (start) await submitRun()
  else await refreshHistory()
}
const startNewQuestion = () => { editingQuestion.value = { id: 0, title: '', prompt: '', built_in: false } }
const saveQuestion = async () => {
  if (!editingQuestion.value || busy.value) return
  const epoch = generation
  busy.value = true
  try { await adminAPI.scheduledTests.saveQuestion({ ...editingQuestion.value }); if (epoch === generation) { editingQuestion.value = null; await loadQuestions() } }
  catch { app.showError(t('admin.intelligence.saveFailed')) }
  finally { busy.value = false }
}
const deleteConfirmed = async () => {
  if (busy.value) return
  const epoch = generation
  busy.value = true
  try {
    if (confirmDeletePlan.value && plan.value) { await adminAPI.scheduledTests.delete(plan.value.id); if (epoch === generation) { plan.value = null; results.value = []; total.value = 0; activeCount.value = 0; Object.assign(form, defaults()) } }
    else if (deletingQuestion.value !== null) { await adminAPI.scheduledTests.deleteQuestion(deletingQuestion.value); if (epoch === generation) await loadQuestions() }
  } catch { app.showError(t('admin.intelligence.saveFailed')) }
  finally { busy.value = false; confirmDeletePlan.value = false; deletingQuestion.value = null }
}
</script>
