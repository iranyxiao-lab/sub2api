import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, fallback?: string) => fallback ?? key,
  }),
}))

import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'

describe('PaymentMethodSelector', () => {
  it('shows the configured display name for custom EasyPay methods', () => {
    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: 'ldc',
        methods: [{ type: 'ldc', display_name: 'LDC Pay', fee_rate: 0, available: true }],
      },
    })

    expect(wrapper.text()).toContain('LDC Pay')
    expect(wrapper.text()).not.toContain('ldc')
    expect(wrapper.text()).not.toContain('payment.methods.ldc')
  })

  it('uses the generic selected style for custom methods that contain built-in names', () => {
    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: 'card_alipay',
        methods: [{ type: 'card_alipay', display_name: 'Card Pay', fee_rate: 0, available: true }],
      },
    })

    const button = wrapper.get('button')
    expect(button.classes()).toContain('border-primary-500')
    expect(button.classes()).not.toContain('border-[#02A9F1]')
  })

  it('shows unlimited daily status without disabling an on-chain method', async () => {
    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: 'usdt_trc20',
        methods: [{
          type: 'usdt_trc20',
          display_name: 'USDT (TRC20)',
          fee_rate: 1,
          available: true,
          unlimited_daily: true,
        }],
      },
    })

    expect(wrapper.text()).toContain('payment.unlimitedDaily')
    expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('select')).toEqual([['usdt_trc20']])
  })
})
