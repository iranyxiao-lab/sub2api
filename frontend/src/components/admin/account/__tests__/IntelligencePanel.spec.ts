import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligencePanel from '../IntelligencePanel.vue'

const { listQuestions, listByAccount, listResults, runNow, saveQuestion, showError } = vi.hoisted(() => ({
  listQuestions: vi.fn(), listByAccount: vi.fn(), listResults: vi.fn(), runNow: vi.fn(), saveQuestion: vi.fn(), showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { scheduledTests: { listQuestions, listByAccount, listResults, runNow, saveQuestion } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

describe('IntelligencePanel', () => {
  it('loads a separate intelligence plan and runs it on demand', async () => {
    const plan = { id: 12, account_id: 8, test_kind: 'intelligence', model_id: 'text-model', cron_expression: '0 9 * * *',
      question_ids: [3], custom_prompt: 'Answer briefly', max_results: 100, enabled: true, next_run_at: null }
    listQuestions.mockResolvedValue([{ id: 3, title: '17 + 25', prompt: '17 + 25?', built_in: true }])
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

  it('shows legacy results without scoring and previews responses in a sandbox', async () => {
    listQuestions.mockResolvedValue([])
    listByAccount.mockResolvedValue([{ id: 13, account_id: 8, test_kind: 'intelligence', model_id: 'text-model', cron_expression: '0 9 * * *', question_ids: [3], custom_prompt: '', max_results: 20, enabled: false }])
    listResults.mockResolvedValue([{ id: 21, plan_id: 13, status: 'success', response_text: '<h1>Example</h1><script>alert(1)</script>', error_message: '', latency_ms: 10,
      question_snapshot: { id: 3, kind: 'choice', title: 'Legacy', prompt: 'Build HTML', answer: 'B', choices: ['Yes', 'No'], built_in: true }, score: 100, grade_status: 'correct', started_at: new Date().toISOString() }])
    const wrapper = mount(IntelligencePanel, { props: { show: false, accountId: 8, modelOptions: [] },
      global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, Icon: true } } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.text()).toContain('Legacy')
    expect(wrapper.text()).toContain('admin.scheduledTests.success')
    expect(wrapper.text()).not.toContain('admin.intelligence.answer')
    expect(wrapper.text()).not.toContain('admin.intelligence.accuracy')
    expect(wrapper.find('input[type="number"]').exists()).toBe(true) // Result retention setting only.
    expect(wrapper.find('input[aria-label="admin.intelligence.score"]').exists()).toBe(false)
    const button = wrapper.findAll('button').find(item => item.text().includes('admin.intelligence.preview'))
    await button!.trigger('click')
    const frame = wrapper.get('iframe')
    expect(frame.attributes('sandbox')).toBe('')
    expect(frame.attributes('srcdoc')).toContain("default-src 'none'")
    wrapper.unmount()
  })

  it('saves a prompt-only question without options or an expected answer', async () => {
    listQuestions.mockResolvedValue([])
    saveQuestion.mockResolvedValue({ id: 30, title: 'HTML', prompt: 'Build a table', built_in: false })
    const wrapper = mount(IntelligencePanel, { props: { show: false, accountId: null, modelOptions: [] },
      global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, Icon: true } } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    const add = wrapper.findAll('button').find(item => item.text().includes('admin.intelligence.addQuestion'))
    await add!.trigger('click')
    await wrapper.get('input[maxlength="160"]').setValue('HTML')
    await wrapper.get('textarea[maxlength="8000"]').setValue('Build a table')
    expect(wrapper.find('select').exists()).toBe(false)
    const save = wrapper.findAll('button').find(item => item.text().includes('common.save'))
    await save!.trigger('click')
    await flushPromises()
    expect(saveQuestion).toHaveBeenCalledWith({ id: 0, title: 'HTML', prompt: 'Build a table', built_in: false })
    wrapper.unmount()
  })
})
