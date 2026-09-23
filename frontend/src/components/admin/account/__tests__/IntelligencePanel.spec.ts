import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligencePanel from '../IntelligencePanel.vue'

const { listQuestions, listByAccount, listResults, runNow, review, showError } = vi.hoisted(() => ({
  listQuestions: vi.fn(), listByAccount: vi.fn(), listResults: vi.fn(), runNow: vi.fn(), review: vi.fn(), showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { scheduledTests: { listQuestions, listByAccount, listResults, runNow, review } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

describe('IntelligencePanel', () => {
  it('loads a separate intelligence plan and runs it on demand', async () => {
    const plan = { id: 12, account_id: 8, test_kind: 'intelligence', model_id: 'text-model', cron_expression: '0 9 * * *',
      question_ids: [3], custom_prompt: 'Answer briefly', max_results: 100, enabled: true, next_run_at: null }
    listQuestions.mockResolvedValue([{ id: 3, title: '17 + 25', kind: 'short_answer', prompt: '17 + 25?', choices: [], answer: '42', rubric: '', built_in: true }])
    listByAccount.mockResolvedValue([{ id: 4, test_kind: 'connectivity' }, plan])
    listResults.mockResolvedValue([])
    runNow.mockResolvedValue({ id: 1, status: 'success' })
    const wrapper = mount(IntelligencePanel, { props: { show: false, accountId: 8, modelOptions: [{ value: 'text-model', label: 'Text' }] },
      global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, Icon: true } } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.text()).toContain('17 + 25')
    const button = wrapper.findAll('button').find(item => item.text().includes('admin.intelligence.runNow'))
    expect(button).toBeDefined()
    await button!.trigger('click')
    await flushPromises()
    expect(runNow).toHaveBeenCalledWith(12)
    expect(listResults).toHaveBeenCalledWith(12, 100)
    wrapper.unmount()
  })

  it('previews open answers only in a sandboxed, network-blocked frame', async () => {
    listQuestions.mockResolvedValue([])
    listByAccount.mockResolvedValue([{ id: 13, account_id: 8, test_kind: 'intelligence', model_id: 'text-model', cron_expression: '0 9 * * *', question_ids: [3], custom_prompt: '', max_results: 20, enabled: false }])
    listResults.mockResolvedValue([{ id: 21, plan_id: 13, status: 'success', response_text: '<h1>Example</h1><script>alert(1)</script>', error_message: '', latency_ms: 10,
      question_snapshot: { id: 3, kind: 'open', title: 'HTML', prompt: 'Build HTML', answer: '', rubric: '', choices: [], built_in: true }, score: null, started_at: new Date().toISOString() }])
    const wrapper = mount(IntelligencePanel, { props: { show: false, accountId: 8, modelOptions: [] },
      global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, Icon: true } } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text().includes('admin.intelligence.preview'))
    await button!.trigger('click')
    const frame = wrapper.get('iframe')
    expect(frame.attributes('sandbox')).toBe('')
    expect(frame.attributes('srcdoc')).toContain("default-src 'none'")
    wrapper.unmount()
  })

  it('allows review of an ambiguous multiple-choice response', async () => {
    listQuestions.mockResolvedValue([])
    listByAccount.mockResolvedValue([{ id: 17, account_id: 8, test_kind: 'intelligence', model_id: 'text-model', cron_expression: '0 9 * * *', question_ids: [3], custom_prompt: '', max_results: 20, enabled: false }])
    listResults.mockResolvedValue([{ id: 23, plan_id: 17, status: 'success', grade_status: 'pending', score: null,
      response_text: 'I think A', error_message: '', latency_ms: 10, started_at: new Date().toISOString(),
      question_snapshot: { id: 3, kind: 'choice', title: 'Choice', prompt: 'Pick A or B', answer: 'A', rubric: '', choices: ['first', 'second'], built_in: true } }])
    review.mockResolvedValue({ id: 23, grade_status: 'reviewed', score: 100 })
    const wrapper = mount(IntelligencePanel, { props: { show: false, accountId: 8, modelOptions: [] },
      global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, Icon: true } } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('summary').trigger('click')
    await wrapper.get('input[aria-label="admin.intelligence.score"]').setValue('100')
    const button = wrapper.findAll('button').find(item => item.text().includes('admin.intelligence.review'))
    await button!.trigger('click')
    await flushPromises()
    expect(review).toHaveBeenCalledWith(17, 23, 100, '')
    wrapper.unmount()
  })
})
