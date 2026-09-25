import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligencePanel from '../IntelligencePanel.vue'

const api = vi.hoisted(() => ({
  listQuestions: vi.fn(), listByAccount: vi.fn(), listRuns: vi.fn(), createRun: vi.fn(), saveQuestion: vi.fn(), getRun: vi.fn(), update: vi.fn(), create: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { scheduledTests: api } }))
const notifications = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ t: (key: string, params?: { seconds?: number }) => params?.seconds === undefined ? key : `${key}:${params.seconds}` }) }))
const plan = { id: 12, account_id: 8, test_kind: 'intelligence', model_id: 'text-model', cron_expression: '0 9 * * *', question_ids: [3], custom_prompt: '', max_results: 100, enabled: false, next_run_at: null }
const result = { id: 21, plan_id: 12, status: 'success', response_text: '<h1>Example</h1><script>alert(1)</script>', error_message: '', latency_ms: 10, question_snapshot: { id: 3, title: 'HTML', prompt: 'Build HTML', built_in: true }, queued_at: '2026-01-01T00:00:00Z', started_at: '2026-01-01T00:00:00Z', finished_at: '2026-01-01T00:00:01Z', trigger_type: 'manual' }
const wrappers: ReturnType<typeof mount>[] = []
const open = (accountId: number | null = 8) => {
  const wrapper = mount(IntelligencePanel, { props: { show: true, accountId, modelOptions: [{ value: 'text-model', label: 'Text' }] }, global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, Icon: true } } })
  wrappers.push(wrapper)
  return wrapper
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('IntersectionObserver', class {
    constructor(private callback: IntersectionObserverCallback) {}
    observe(target: Element) { this.callback([{ isIntersecting: true, target } as IntersectionObserverEntry], this as unknown as IntersectionObserver) }
    disconnect() {}
    unobserve() {}
  })
  api.listQuestions.mockResolvedValue([{ id: 3, title: 'HTML', prompt: 'Build HTML', built_in: true }])
  api.listByAccount.mockResolvedValue([plan])
  api.listRuns.mockResolvedValue({ items: [result], total: 1, active_count: 0 })
  api.update.mockResolvedValue(plan)
  api.create.mockResolvedValue(plan)
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.useRealTimers(); vi.unstubAllGlobals() })

describe('IntelligencePanel', () => {
  const button = (wrapper: ReturnType<typeof open>, key: string) => wrapper.findAll('button').find(b => b.text() === key)!
  const selectOtherQuestion = async (wrapper: ReturnType<typeof open>) => {
    await button(wrapper, 'admin.intelligence.settings').trigger('click')
    await wrapper.get('input[type="radio"][value="4"]').setValue()
  }

  const twoQuestions = () => api.listQuestions.mockResolvedValue([
    { id: 3, title: 'HTML', prompt: 'Build HTML', built_in: true },
    { id: 4, title: 'Arithmetic', prompt: '17 + 25', built_in: true }
  ])

  it('selects exactly one question and saves that selection before running from history', async () => {
    twoQuestions()
    api.update.mockImplementation(async (_id, data) => ({ ...plan, ...data }))
    api.createRun.mockResolvedValue({ ...result, id: 22, status: 'queued', question_snapshot: { id: 4, title: 'Arithmetic', prompt: '17 + 25' } })
    const wrapper = open()
    await flushPromises()
    await selectOtherQuestion(wrapper)
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(0)
    expect((wrapper.get('input[value="3"]').element as HTMLInputElement).checked).toBe(false)
    expect((wrapper.get('input[value="4"]').element as HTMLInputElement).checked).toBe(true)
    await button(wrapper, 'admin.intelligence.history').trigger('click')
    await button(wrapper, 'admin.intelligence.runNow').trigger('click')
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith(12, expect.objectContaining({ question_ids: [4] }))
    expect(api.update.mock.invocationCallOrder[0]).toBeLessThan(api.createRun.mock.invocationCallOrder[0])
    expect(wrapper.get('[data-run-id="22"]').text()).toContain('Arithmetic')
  })

  it('waits for saving the current prompt and question and does not submit old settings on failure', async () => {
    twoQuestions()
    let reject!: (error: Error) => void
    api.update.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail }))
    const wrapper = open()
    await flushPromises()
    await selectOtherQuestion(wrapper)
    await wrapper.get('textarea[maxlength="4000"]').setValue('Explain briefly')
    await button(wrapper, 'admin.intelligence.runNow').trigger('click')
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith(12, expect.objectContaining({ question_ids: [4], custom_prompt: 'Explain briefly' }))
    expect(api.createRun).not.toHaveBeenCalled()
    expect(wrapper.find('fieldset[disabled]').exists()).toBe(true)
    reject(new Error('save failed'))
    await flushPromises()
    expect(api.createRun).not.toHaveBeenCalled()
    expect(notifications.showError).toHaveBeenCalledWith('admin.intelligence.saveFailed')
    expect(wrapper.find('fieldset[disabled]').exists()).toBe(false)
  })

  it('requires an explicit single choice for legacy multi-question plans', async () => {
    twoQuestions()
    api.listByAccount.mockResolvedValue([{ ...plan, question_ids: [3, 4] }])
    const wrapper = open()
    await flushPromises()
    await button(wrapper, 'admin.intelligence.runNow').trigger('click')
    expect(wrapper.text()).toContain('admin.intelligence.chooseSingleQuestion')
    expect(wrapper.findAll('input[type="radio"]:checked')).toHaveLength(0)
    expect(api.update).not.toHaveBeenCalled()
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('does not enqueue a switched account after a pending save completes', async () => {
    twoQuestions()
    let resolve!: (value: unknown) => void
    api.update.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = open()
    await flushPromises()
    await selectOtherQuestion(wrapper)
    await button(wrapper, 'admin.intelligence.runNow').trigger('click')
    await flushPromises()
    await wrapper.setProps({ accountId: 9 })
    resolve({ ...plan, question_ids: [4] })
    await flushPromises()
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('blocks changed selections while a previous submission remains unconfirmed', async () => {
    twoQuestions()
    api.createRun.mockRejectedValueOnce(new Error('network disconnected'))
    const wrapper = open()
    await flushPromises()
    await button(wrapper, 'admin.intelligence.runNow').trigger('click')
    await flushPromises()
    await selectOtherQuestion(wrapper)
    await button(wrapper, 'admin.intelligence.runNow').trigger('click')
    await flushPromises()
    expect(api.update).not.toHaveBeenCalled()
    expect(api.createRun).toHaveBeenCalledTimes(1)
    expect(notifications.showError).toHaveBeenCalledWith('admin.intelligence.pendingSubmission')
  })

  const httpCrypto = () => vi.stubGlobal('crypto', {
    getRandomValues: (bytes: Uint8Array) => bytes.fill(42)
  })

  it('submits run immediately on HTTP where randomUUID is unavailable', async () => {
    httpCrypto()
    api.createRun.mockResolvedValue({ ...result, id: 22, status: 'queued', response_text: '' })
    const wrapper = open()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text().includes('admin.intelligence.runNow'))!.trigger('click')
    await flushPromises()
    expect(api.createRun).toHaveBeenCalledWith(12, expect.stringMatching(/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/))
    expect(api.update).not.toHaveBeenCalled()
    expect(wrapper.find('[data-run-id="22"]').exists()).toBe(true)
    expect(notifications.showError).not.toHaveBeenCalled()
  })

  it('saves and starts on HTTP without misreporting a save failure', async () => {
    httpCrypto()
    api.createRun.mockResolvedValue({ ...result, id: 22, status: 'queued', response_text: '' })
    const wrapper = open()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.intelligence.settings')!.trigger('click')
    await wrapper.findAll('button').find(b => b.text() === 'admin.intelligence.saveAndRun')!.trigger('click')
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith(12, expect.objectContaining({ model_id: 'text-model' }))
    expect(api.createRun).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-run-id="22"]').exists()).toBe(true)
    expect(notifications.showError).not.toHaveBeenCalled()
  })

  it('reports local submission failure separately and releases the busy state', async () => {
    vi.stubGlobal('crypto', undefined)
    const wrapper = open()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.intelligence.settings')!.trigger('click')
    const button = wrapper.findAll('button').find(b => b.text() === 'admin.intelligence.saveAndRun')!
    await button.trigger('click')
    await flushPromises()
    expect(api.update).toHaveBeenCalledTimes(1)
    expect(api.createRun).not.toHaveBeenCalled()
    expect(notifications.showError).toHaveBeenCalledWith('admin.intelligence.requestIdFailed')
    expect(notifications.showError).not.toHaveBeenCalledWith('admin.intelligence.saveFailed')
    expect(button.attributes('disabled')).toBeUndefined()
  })

  it('loads history on initial mount and renders HTML inline without clicking details', async () => {
    const wrapper = open()
    await flushPromises()
    expect(api.listRuns).toHaveBeenCalledWith(12, 1, '', expect.any(AbortSignal))
    const frame = wrapper.get('iframe')
    expect(frame.attributes('sandbox')).toBe('')
    expect(frame.attributes('srcdoc')).toContain("default-src 'none'")
    expect(frame.attributes('srcdoc')).toContain('<h1>Example</h1>')
    expect(wrapper.find('input[aria-label="admin.intelligence.score"]').exists()).toBe(false)
    expect(api.createRun).not.toHaveBeenCalled()
  })

  it('loads history independently when question discovery fails', async () => {
    api.listQuestions.mockRejectedValue(new Error('unavailable'))
    const wrapper = open()
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('admin.intelligence.noRuns')
  })

  it('does not show empty history while loading', async () => {
    let resolve!: (value: unknown) => void
    api.listRuns.mockReturnValue(new Promise(r => { resolve = r }))
    const wrapper = open()
    await flushPromises()
    expect(wrapper.text()).toContain('common.loading')
    expect(wrapper.text()).not.toContain('admin.intelligence.noRuns')
    resolve({ items: [], total: 0, active_count: 0 })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.intelligence.noRuns')
  })

  it('shows a persisted queued record immediately and prevents a second submission', async () => {
    api.createRun.mockResolvedValue({ ...result, id: 22, status: 'queued', response_text: '', started_at: null, finished_at: null })
    const wrapper = open()
    await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text().includes('admin.intelligence.runNow'))!
    await button.trigger('click')
    await flushPromises()
    expect(api.createRun).toHaveBeenCalledWith(12, expect.stringMatching(/^[a-f0-9-]{36}$/))
    expect(wrapper.text()).toContain('admin.intelligence.state_queued')
    expect(wrapper.find('[data-run-id="22"]').exists()).toBe(true)
    await button.trigger('click')
    expect(api.createRun).toHaveBeenCalledTimes(1)
  })

  it('shows failed partial responses as well as the error', async () => {
    api.listRuns.mockResolvedValue({ items: [{ ...result, status: 'failed', error_code: 'execution_timeout', error_message: 'deadline exceeded' }], total: 1, active_count: 0 })
    const wrapper = open()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.intelligence.partialOutput')
    expect(wrapper.text()).toContain('deadline exceeded')
    expect(wrapper.find('iframe').exists()).toBe(true)
  })

  it.each([120, 300, 600])('shows the timeout saved on the run (%s seconds)', async seconds => {
    api.listRuns.mockResolvedValue({ items: [{ ...result, status: 'failed', execution_timeout_seconds: seconds, error_code: 'execution_timeout', error_message: 'deadline exceeded' }], total: 1, active_count: 0 })
    const wrapper = open()
    await flushPromises()
    expect(wrapper.text()).toContain(`admin.intelligence.error_execution_timeout:${seconds}`)
    expect(wrapper.text()).toContain(`admin.intelligence.executionLimit:${seconds}`)
  })

  it('rejects stale account history when switching accounts', async () => {
    let resolve!: (value: unknown) => void
    api.listRuns.mockReturnValueOnce(new Promise(r => { resolve = r }))
    const wrapper = open()
    await flushPromises()
    api.listByAccount.mockResolvedValue([{ ...plan, id: 13, account_id: 9 }])
    api.listRuns.mockResolvedValue({ items: [], total: 0, active_count: 0 })
    await wrapper.setProps({ accountId: 9 })
    await flushPromises()
    resolve({ items: [result], total: 1, active_count: 0 })
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.intelligence.noRuns')
  })

  it('saves prompt-only questions', async () => {
    api.saveQuestion.mockResolvedValue({ id: 30 })
    const wrapper = open(null)
    await flushPromises()
    await wrapper.findAll('button').find(item => item.text().includes('admin.intelligence.addQuestion'))!.trigger('click')
    await wrapper.get('input[maxlength="160"]').setValue('Example')
    await wrapper.get('textarea[maxlength="8000"]').setValue('Build a table')
    await wrapper.findAll('button').find(item => item.text().includes('common.save'))!.trigger('click')
    await flushPromises()
    expect(api.saveQuestion).toHaveBeenCalledWith({ id: 0, title: 'Example', prompt: 'Build a table', built_in: false })
  })

  it('reuses the idempotency key when acceptance is unknown', async () => {
    api.createRun.mockRejectedValueOnce(new Error('network disconnected')).mockResolvedValueOnce({ ...result, id: 22, status: 'queued', response_text: '' })
    const wrapper = open()
    await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text().includes('admin.intelligence.runNow'))!
    await button.trigger('click')
    await flushPromises()
    await button.trigger('click')
    await flushPromises()
    expect(api.createRun).toHaveBeenCalledTimes(2)
    expect(api.createRun.mock.calls[0][1]).toBe(api.createRun.mock.calls[1][1])
  })

  it('polls active work and pauses when the document is hidden', async () => {
    vi.useFakeTimers()
    api.listRuns.mockResolvedValue({ items: [{ ...result, status: 'running', response_text: '' }], total: 1, active_count: 1 })
    open()
    await flushPromises()
    expect(api.listRuns).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(api.listRuns).toHaveBeenCalledTimes(2)
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(16000)
    expect(api.listRuns).toHaveBeenCalledTimes(2)
    hidden.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(api.listRuns).toHaveBeenCalledTimes(3)
    hidden.mockRestore()
  })
})
