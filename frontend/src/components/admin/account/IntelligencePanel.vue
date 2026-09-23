<template>
  <BaseDialog :show="show" :title="accountId ? t('admin.intelligence.title') : t('admin.intelligence.bank')" width="wide" @close="emit('close')">
    <div class="max-h-[75vh] space-y-4 overflow-y-auto text-sm text-gray-800 dark:text-gray-200">
      <div v-if="!accountId" class="space-y-3">
        <button class="btn btn-primary" @click="startNewQuestion"><Icon name="plus" size="sm" /> {{ t('admin.intelligence.addQuestion') }}</button>
        <div v-if="editingQuestion" class="space-y-3 border-b border-gray-200 pb-4 dark:border-dark-600">
          <label class="block">{{ t('admin.intelligence.questionTitle') }}<input v-model="editingQuestion.title" maxlength="160" class="input mt-1 w-full" /></label>
          <label class="block">{{ t('admin.intelligence.kind') }}
            <select v-model="editingQuestion.kind" class="input mt-1 w-full"><option value="choice">{{ t('admin.intelligence.choice') }}</option><option value="short_answer">{{ t('admin.intelligence.shortAnswer') }}</option><option value="open">{{ t('admin.intelligence.open') }}</option></select>
          </label>
          <label class="block">{{ t('admin.intelligence.prompt') }}<textarea v-model="editingQuestion.prompt" maxlength="8000" rows="4" class="input mt-1 w-full" /></label>
          <label v-if="editingQuestion.kind === 'choice'" class="block">{{ t('admin.intelligence.choices') }}<textarea v-model="choicesText" rows="4" class="input mt-1 w-full" /></label>
          <label v-if="editingQuestion.kind !== 'open'" class="block">{{ t('admin.intelligence.answer') }}<input v-model="editingQuestion.answer" class="input mt-1 w-full" /></label>
          <label v-else class="block">{{ t('admin.intelligence.rubric') }}<textarea v-model="editingQuestion.rubric" rows="2" class="input mt-1 w-full" /></label>
          <div class="flex gap-2"><button class="btn btn-primary" :disabled="busy" @click="saveQuestion">{{ t('common.save') }}</button><button class="btn btn-secondary" @click="editingQuestion = null">{{ t('common.cancel') }}</button></div>
        </div>
        <div v-for="question in questions" :key="question.id" class="flex items-start justify-between gap-3 border-b border-gray-200 py-2 dark:border-dark-600">
          <div class="min-w-0"><p class="font-medium">{{ question.title }} <span class="text-xs text-gray-500">{{ t(`admin.intelligence.${question.kind === 'short_answer' ? 'shortAnswer' : question.kind}`) }}</span></p><p class="whitespace-pre-wrap break-words text-gray-500">{{ question.prompt }}</p></div>
          <div v-if="!question.built_in" class="flex shrink-0 gap-1">
            <button :title="t('common.edit')" class="p-2" @click="editQuestion(question)"><Icon name="edit" size="sm" /></button>
            <button :title="t('common.delete')" class="p-2 text-red-600" @click="removeQuestion(question.id)"><Icon name="trash" size="sm" /></button>
          </div>
        </div>
      </div>
      <template v-else>
        <div class="grid gap-3 sm:grid-cols-2">
          <label>{{ t('admin.scheduledTests.model') }}<select v-model="form.model_id" class="input mt-1 w-full"><option value="">{{ t('admin.intelligence.selectModel') }}</option><option v-for="model in modelOptions" :key="String(model.value)" :value="model.value">{{ model.label }}</option></select></label>
          <label>{{ t('admin.scheduledTests.cronExpression') }}<input v-model="form.cron_expression" class="input mt-1 w-full" placeholder="0 9 * * *" /></label>
          <label class="sm:col-span-2">{{ t('admin.intelligence.customPrompt') }}<textarea v-model="form.custom_prompt" maxlength="4000" rows="2" class="input mt-1 w-full" /></label>
          <fieldset class="sm:col-span-2"><legend class="font-medium">{{ t('admin.intelligence.selectQuestions') }}</legend>
            <label v-for="question in questions" :key="question.id" class="flex items-center gap-2 py-1"><input v-model="form.question_ids" type="checkbox" :value="question.id" /><span>{{ question.title }}</span></label>
            <button class="text-primary-600" @click="emit('open-bank')">{{ t('admin.intelligence.manageBank') }}</button>
          </fieldset>
          <label>{{ t('admin.scheduledTests.maxResults') }}<input v-model.number="form.max_results" type="number" min="1" max="200" class="input mt-1 w-full" /></label>
          <label class="flex items-center gap-2"><input v-model="form.enabled" type="checkbox" />{{ t('admin.scheduledTests.enabled') }}</label>
        </div>
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-primary" :disabled="busy || !form.model_id || !form.question_ids.length" @click="savePlan">{{ t('common.save') }}</button>
          <button v-if="plan" class="btn btn-secondary" :disabled="busy" @click="runNow"><Icon name="play" size="sm" /> {{ t('admin.intelligence.runNow') }}</button>
          <button v-if="plan" class="btn btn-secondary text-red-600" :disabled="busy" @click="deletePlan">{{ t('common.delete') }}</button>
        </div>
        <div v-if="plan" class="border-t border-gray-200 pt-3 dark:border-dark-600">
          <div class="flex flex-wrap justify-between gap-2"><h3 class="font-semibold">{{ t('admin.intelligence.history') }}</h3><span class="text-xs text-gray-500">{{ t('admin.scheduledTests.nextRun') }}: {{ plan.next_run_at ? formatDateTime(plan.next_run_at) : '-' }}</span></div>
          <p v-if="graded.length" class="my-2 text-sm">{{ t('admin.intelligence.accuracy') }}: {{ Math.round(graded.filter(item => item.score === 100).length / graded.length * 100) }}% ({{ graded.length }})</p>
          <p v-if="!results.length" class="py-4 text-center text-gray-500">{{ t('admin.scheduledTests.noResults') }}</p>
          <div v-for="result in results" :key="result.id" class="border-b border-gray-200 py-3 dark:border-dark-600">
            <div class="flex flex-wrap items-center justify-between gap-2"><span class="font-medium">{{ result.question_snapshot?.title || '-' }} · {{ result.status === 'failed' ? t('admin.scheduledTests.failed') : gradeLabel(result) }} · {{ result.latency_ms }}ms</span><span class="text-xs text-gray-500">{{ formatDateTime(result.started_at) }}</span></div>
            <details class="mt-2"><summary class="cursor-pointer text-primary-600">{{ t('admin.intelligence.details') }}</summary>
              <p class="mt-2 whitespace-pre-wrap break-words text-gray-500">{{ result.prompt_snapshot }}</p>
              <pre class="mt-2 max-h-52 overflow-auto whitespace-pre-wrap break-all bg-gray-50 p-2 text-xs dark:bg-dark-900">{{ result.error_message || result.response_text }}</pre>
              <p v-if="result.question_snapshot?.kind !== 'open'">{{ t('admin.intelligence.answer') }}: {{ result.question_snapshot?.answer }}</p>
              <p v-if="result.review_note">{{ result.review_note }}</p>
              <button v-if="result.question_snapshot?.kind === 'open' && result.response_text" class="btn btn-secondary my-2" @click="preview = htmlPreview(result.response_text)">{{ t('admin.intelligence.preview') }}</button>
              <div v-if="result.status === 'success' && (result.question_snapshot?.kind === 'open' || result.grade_status === 'pending')" class="flex flex-wrap items-center gap-2"><input v-model.number="reviewScores[result.id]" type="number" min="0" max="100" class="input w-20" :aria-label="t('admin.intelligence.score')" /><input v-model="reviewNotes[result.id]" class="input flex-1" :placeholder="t('admin.intelligence.reviewNote')" /><button class="btn btn-secondary" :disabled="!validReviewScore(result.id)" @click="review(result)">{{ t('admin.intelligence.review') }}</button></div>
            </details>
          </div>
        </div>
      </template>
    </div>
    <div v-if="preview !== null" class="fixed inset-0 z-[10000] flex flex-col bg-white p-4 dark:bg-dark-900">
      <div class="mb-2 flex justify-between"><strong>{{ t('admin.intelligence.preview') }}</strong><button :title="t('common.close')" @click="preview = null"><Icon name="x" size="sm" /></button></div>
      <iframe :srcdoc="preview" sandbox="" referrerpolicy="no-referrer" class="min-h-0 flex-1 border border-gray-300 bg-white" :title="t('admin.intelligence.preview')" />
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { buildStaticHtmlPreview } from '@/utils/intelligencePreview'
import type { SelectOption } from '@/components/common/Select.vue'
import type { IntelligenceQuestion, ScheduledTestPlan, ScheduledTestResult } from '@/types'

const props = defineProps<{ show: boolean; accountId: number | null; modelOptions: SelectOption[] }>()
const emit = defineEmits<{ close: []; 'open-bank': [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const questions = ref<IntelligenceQuestion[]>([])
const plan = ref<ScheduledTestPlan | null>(null)
const results = ref<ScheduledTestResult[]>([])
const editingQuestion = ref<IntelligenceQuestion | null>(null)
const choicesText = ref('')
const busy = ref(false)
const preview = ref<string | null>(null)
const reviewScores = reactive<Record<number, number>>({})
const reviewNotes = reactive<Record<number, string>>({})
const validReviewScore = (id: number) => Number.isInteger(reviewScores[id]) && reviewScores[id] >= 0 && reviewScores[id] <= 100
const form = reactive({ model_id: '', cron_expression: '0 9 * * *', question_ids: [] as number[], custom_prompt: '', max_results: 100, enabled: true })
const graded = computed(() => results.value.filter(item => (item.grade_status === 'correct' || item.grade_status === 'incorrect') && item.status === 'success'))
const gradeLabel = (item: ScheduledTestResult) => item.grade_status === 'correct' ? t('admin.intelligence.correct') : item.grade_status === 'incorrect' ? t('admin.intelligence.incorrect') : item.score !== null ? `${item.score}/100` : t('admin.intelligence.pending')

const load = async () => {
  try {
    questions.value = await adminAPI.scheduledTests.listQuestions()
    if (props.accountId) {
      const plans = await adminAPI.scheduledTests.listByAccount(props.accountId)
      plan.value = plans.find(item => item.test_kind === 'intelligence') ?? null
      if (plan.value) {
        Object.assign(form, { model_id: plan.value.model_id, cron_expression: plan.value.cron_expression, question_ids: [...plan.value.question_ids], custom_prompt: plan.value.custom_prompt, max_results: plan.value.max_results, enabled: plan.value.enabled })
        await loadResults()
      } else { results.value = []; Object.assign(form, { model_id: '', cron_expression: '0 9 * * *', question_ids: [], custom_prompt: '', max_results: 100, enabled: true }) }
    }
  } catch (error: any) { appStore.showError(error?.message || t('admin.intelligence.loadFailed')) }
}
watch(() => [props.show, props.accountId], ([visible]) => { if (visible) void load(); else preview.value = null })
const loadResults = async () => { if (plan.value) results.value = await adminAPI.scheduledTests.listResults(plan.value.id, Math.min(plan.value.max_results, 200)) }
const startNewQuestion = () => { editingQuestion.value = { id: 0, title: '', kind: 'choice', prompt: '', choices: [], answer: '', rubric: '', built_in: false }; choicesText.value = '' }
const editQuestion = (question: IntelligenceQuestion) => { editingQuestion.value = { ...question, choices: [...question.choices] }; choicesText.value = question.choices.join('\n') }
const saveQuestion = async () => {
  if (!editingQuestion.value) return
  busy.value = true
  try { await adminAPI.scheduledTests.saveQuestion({ ...editingQuestion.value, choices: editingQuestion.value.kind === 'choice' ? choicesText.value.split('\n').map(v => v.trim()).filter(Boolean) : [] }); editingQuestion.value = null; await load() }
  catch (error: any) { appStore.showError(error?.message || t('admin.intelligence.saveFailed')) }
  finally { busy.value = false }
}
const removeQuestion = async (id: number) => { if (!window.confirm(t('admin.intelligence.confirmDelete'))) return; try { await adminAPI.scheduledTests.deleteQuestion(id); await load() } catch (error: any) { appStore.showError(error?.message || t('admin.intelligence.saveFailed')) } }
const savePlan = async () => {
  if (!props.accountId) return
  busy.value = true
  try {
    const data = { ...form, account_id: props.accountId, test_kind: 'intelligence' as const, auto_recover: false }
    plan.value = plan.value ? await adminAPI.scheduledTests.update(plan.value.id, data) : await adminAPI.scheduledTests.create(data)
    appStore.showSuccess(t('admin.scheduledTests.updateSuccess'))
    await load()
  } catch (error: any) { appStore.showError(error?.message || t('admin.intelligence.saveFailed')) }
  finally { busy.value = false }
}
const runNow = async () => { if (!plan.value) return; busy.value = true; try { await adminAPI.scheduledTests.runNow(plan.value.id); await loadResults() } catch (error: any) { appStore.showError(error?.message || t('admin.intelligence.runFailed')) } finally { busy.value = false } }
const deletePlan = async () => { if (!plan.value || !window.confirm(t('admin.scheduledTests.confirmDelete'))) return; busy.value = true; try { await adminAPI.scheduledTests.delete(plan.value.id); await load() } catch (error: any) { appStore.showError(error?.message || t('admin.intelligence.saveFailed')) } finally { busy.value = false } }
const review = async (result: ScheduledTestResult) => { if (!plan.value) return; try { await adminAPI.scheduledTests.review(plan.value.id, result.id, reviewScores[result.id], reviewNotes[result.id] || ''); await loadResults() } catch (error: any) { appStore.showError(error?.message || t('admin.intelligence.saveFailed')) } }
const htmlPreview = buildStaticHtmlPreview
</script>
