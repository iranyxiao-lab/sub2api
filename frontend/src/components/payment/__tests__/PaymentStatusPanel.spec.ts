import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const pollOrderStatus = vi.hoisted(() => vi.fn())
const cancelOrder = vi.hoisted(() => vi.fn())
const verifyOrder = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const showSuccess = vi.hoisted(() => vi.fn())
const toCanvas = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    pollOrderStatus,
  }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    cancelOrder,
    verifyOrder,
  },
}))

vi.mock('qrcode', () => ({
  default: {
    toCanvas,
  },
}))

import PaymentStatusPanel from '../PaymentStatusPanel.vue'

const orderFactory = (status: string) => ({
  id: 42,
  user_id: 9,
  amount: 88,
  pay_amount: 88,
  fee_rate: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_20260420abcd1234',
  status,
  order_type: 'balance',
  created_at: '2026-04-20T12:00:00Z',
  expires_at: '2099-01-01T12:30:00Z',
  refund_amount: 0,
})

const onchainPaymentFactory = (overrides: Record<string, unknown> = {}) => ({
  network: 'tron-mainnet',
  chain_id: 728126428,
  token: 'USDT',
  token_contract: 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t',
  address: 'TExampleDepositAddress',
  amount: '88',
  qr_code: 'TExampleDepositAddress',
  received_amount: '0',
  pending_amount: '88',
  expires_at: '2099-01-01T12:30:00Z',
  status: 'PENDING',
  ...overrides,
})

describe('PaymentStatusPanel', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    pollOrderStatus.mockReset()
    cancelOrder.mockReset()
    verifyOrder.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    toCanvas.mockReset().mockResolvedValue(undefined)
  })

  it('renders a TRC20 address-only QR and copies address and amount independently', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'TExampleDepositAddress',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'usdt_trc20',
        orderType: 'balance',
        onchainPayment: {
          network: 'tron-mainnet',
          chain_id: 728126428,
          token: 'USDT',
          token_contract: 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t',
          address: 'TExampleDepositAddress',
          amount: '88.125001',
          qr_code: 'TExampleDepositAddress',
          received_amount: '0',
          pending_amount: '88.125001',
          expires_at: '2099-01-01T12:30:00Z',
          status: 'PENDING',
        },
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.get('[data-test="onchain-payment-panel"]').text()).toContain('TRON (TRC20)')
    expect(wrapper.get('[data-test="onchain-address"]').text()).toBe('TExampleDepositAddress')
    expect(wrapper.text()).toContain('88.125001 USDT')
    expect(wrapper.text()).toContain('payment.onchain.trc20RiskWarning')
    expect(toCanvas).toHaveBeenCalledWith(
      expect.any(HTMLCanvasElement),
      'TExampleDepositAddress',
      expect.any(Object),
    )

    await wrapper.get('[data-test="copy-onchain-address"]').trigger('click')
    await wrapper.get('[data-test="copy-onchain-amount"]').trigger('click')
    await flushPromises()

    expect(writeText).toHaveBeenNthCalledWith(1, 'TExampleDepositAddress')
    expect(writeText).toHaveBeenNthCalledWith(2, '88.125001')
    expect(showSuccess).toHaveBeenCalledTimes(2)
  })

  it('labels ERC20 payments and shows the Ethereum network warning', async () => {
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 43,
        qrCode: '0x1111111111111111111111111111111111111111',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'usdt_erc20',
        orderType: 'balance',
        onchainPayment: {
          network: 'ethereum-mainnet',
          chain_id: 1,
          token: 'USDT',
          token_contract: '0xdAC17F958D2ee523a2206206994597C13D831ec7',
          address: '0x1111111111111111111111111111111111111111',
          amount: '200',
          qr_code: '0x1111111111111111111111111111111111111111',
          received_amount: '0',
          pending_amount: '200',
          expires_at: '2099-01-01T12:30:00Z',
        },
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('Ethereum (ERC20)')
    expect(wrapper.text()).toContain('payment.onchain.erc20RiskWarning')
  })

  it('refreshes partial on-chain amounts without treating the order as complete', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('PARTIALLY_PAID'),
      payment_type: 'usdt_trc20',
      onchain_payment: onchainPaymentFactory({
        received_amount: '10.25',
        pending_amount: '77.75',
        status: 'PARTIALLY_PAID',
      }),
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'TExampleDepositAddress',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'usdt_trc20',
        orderType: 'balance',
        onchainPayment: onchainPaymentFactory(),
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.get('[data-test="onchain-status"]').text()).toContain('payment.onchain.status.partial')
    expect(wrapper.text()).toContain('10.25 USDT')
    expect(wrapper.text()).toContain('77.75 USDT')
    expect(wrapper.emitted('success')).toBeUndefined()
  })

  it('keeps an on-chain RECHARGING order in processing until credit completes', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('RECHARGING'),
      payment_type: 'usdt_trc20',
      onchain_payment: onchainPaymentFactory({
        received_amount: '88',
        pending_amount: '0',
        status: 'CREDIT_PENDING',
      }),
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'TExampleDepositAddress',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'usdt_trc20',
        orderType: 'balance',
        onchainPayment: onchainPaymentFactory(),
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.get('[data-test="onchain-status"]').text()).toContain('payment.onchain.status.processing')
    expect(wrapper.text()).not.toContain('payment.result.success')
    expect(wrapper.emitted('success')).toBeUndefined()
  })

  it('settles an on-chain order only after the order is completed', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('COMPLETED'),
      payment_type: 'usdt_trc20',
      onchain_payment: onchainPaymentFactory({
        received_amount: '88',
        pending_amount: '0',
        status: 'SETTLED',
      }),
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'TExampleDepositAddress',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'usdt_trc20',
        orderType: 'balance',
        onchainPayment: onchainPaymentFactory(),
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('shows review state for expired underpayment and preserves the payment details', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('REVIEW_REQUIRED'),
      payment_type: 'usdt_trc20',
      onchain_payment: onchainPaymentFactory({
        received_amount: '10',
        pending_amount: '78',
        status: 'REVIEW_REQUIRED',
      }),
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'TExampleDepositAddress',
        expiresAt: '2024-01-01T12:30:00Z',
        paymentType: 'usdt_trc20',
        orderType: 'balance',
        onchainPayment: onchainPaymentFactory({ expires_at: '2024-01-01T12:30:00Z' }),
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.get('[data-test="onchain-status"]').text()).toContain('payment.onchain.status.review')
    expect(wrapper.get('[data-test="onchain-address"]').text()).toBe('TExampleDepositAddress')
    expect(wrapper.text()).toContain('10 USDT')
  })

  it('shows temporary unavailability after repeated poll failures and continues retrying', async () => {
    pollOrderStatus.mockResolvedValue(null)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'TExampleDepositAddress',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'usdt_trc20',
        orderType: 'balance',
        onchainPayment: onchainPaymentFactory(),
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(6000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledTimes(3)
    expect(wrapper.get('[data-test="onchain-status"]').text()).toContain('payment.onchain.status.unavailable')

    await vi.advanceTimersByTimeAsync(3000)
    expect(pollOrderStatus).toHaveBeenCalledTimes(4)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('treats RECHARGING as a successful terminal state', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('RECHARGING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('shows reopen button in QR mode when payUrl is also available', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({ closed: false } as Window)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        payUrl: 'https://pay.example.com/session/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('payment.qr.openPayWindow')

    await wrapper.get('button.btn.btn-secondary.text-sm').trigger('click')
    expect(openSpy).toHaveBeenCalledWith(
      'https://pay.example.com/session/42',
      'paymentPopup',
      expect.any(String),
    )

    openSpy.mockRestore()
  })

  it('uses generic QR copy for custom methods that contain built-in names', async () => {
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'card_alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.qr.scanToPay')
    expect(wrapper.text()).not.toContain('payment.qr.scanAlipay')
  })

  it('actively verifies a stuck pending order and settles it when upstream confirms payment', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('actively verifies a pending mobile Alipay precreate order', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign: vi.fn() },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => false,
    })
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({ data: orderFactory('COMPLETED') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.emitted('success')).toHaveLength(1)

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  it('keeps the QR fallback hidden until the Alipay app launch times out', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    const assign = vi.fn()
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => false,
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    expect(assign).toHaveBeenCalledWith(expect.stringContaining('alipays://platformapi/startapp?saId=10000007&qrcode='))
    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(false)

    await vi.advanceTimersByTimeAsync(2200)
    await flushPromises()

    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('payment.qr.saveQRCode')
    expect(wrapper.text()).toContain('sub2_20260420abcd1234')
    expect(toCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), 'https://qr.alipay.com/dynamic-order-42', expect.any(Object))

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  it('does not show the QR fallback after the page enters the background', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    let hidden = false
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign: vi.fn() },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => hidden,
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    hidden = true
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(2200)
    await flushPromises()

    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('payment.qr.alipayContinueInApp')

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })
})
