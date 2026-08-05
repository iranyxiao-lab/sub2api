import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const apiMocks = vi.hoisted(() => ({
  getOnchainHealth: vi.fn(),
  getOnchainDeposits: vi.fn(),
  getOnchainReviews: vi.fn(),
}))
const showError = vi.hoisted(() => vi.fn())

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: apiMocks,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, fallback?: unknown) => typeof fallback === 'string' ? fallback : key,
  }),
}))

import AdminOnchainOperations from '../AdminOnchainOperations.vue'

const deposit = {
  id: 1,
  intent_id: 11,
  payment_order_id: 21,
  user_id: 31,
  network: 'tron-mainnet',
  chain_id: 0,
  transaction_id: 'tx-admin-operations',
  log_index: 4,
  transaction_index: 2,
  block_height: 12345,
  block_hash: 'block-hash',
  token_contract: 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t',
  from_address: 'sender-address',
  to_address: 'deposit-address',
  amount_raw: '5000000',
  transaction_time: '2026-07-31T10:00:00Z',
  finalized: true,
  status: 'CREDITED',
  created_at: '2026-07-31T10:00:00Z',
  updated_at: '2026-07-31T10:00:00Z',
}

describe('AdminOnchainOperations', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    apiMocks.getOnchainHealth.mockResolvedValue({
      data: [{
        network: 'tron-mainnet',
        chain_id: 0,
        node_healthy: true,
        full_node_height: 12360,
        solid_height: 12355,
        node_block_lag: 5,
        cursor_health: 'HEALTHY',
        finalized_height: 12345,
        scan_lag_blocks: 10,
        scan_lag_seconds: 90,
        last_success_at: '2026-07-31T10:00:00Z',
        pending_settlements: 2,
        review_required: 1,
        unswept_balance_raw: '45000000',
        resource_wait_sweeps: 2,
        sweep_retry_count: 4,
        sweep_max_retry_count: 3,
        sweep_failure_count: 1,
        trx_balance_sun: 3000000,
        available_energy: 75000,
        available_bandwidth: 1200,
        signer_healthy: true,
        alerts: [{ code: 'SETTLEMENT_BACKLOG', severity: 'P1', value: '2', threshold: '2' }],
        hot_wallet_warning: false,
        cold_transfer_approval_required: false,
        automatic_sweeps_paused: false,
        updated_at: '2026-07-31T10:00:00Z',
      }],
    })
    apiMocks.getOnchainDeposits.mockResolvedValue({
      data: { items: [deposit], total: 1, page: 1, page_size: 10 },
    })
    apiMocks.getOnchainReviews.mockResolvedValue({
      data: { items: [{ ...deposit, id: 2, status: 'REVIEW_REQUIRED' }], total: 1, page: 1, page_size: 10 },
    })
  })

  it('renders node and scanner health with the durable deposit ledger', async () => {
    const wrapper = mount(AdminOnchainOperations, {
      global: { stubs: { Icon: true, Pagination: true } },
    })
    await flushPromises()

    expect(apiMocks.getOnchainHealth).toHaveBeenCalledOnce()
    expect(apiMocks.getOnchainDeposits).toHaveBeenCalledWith(expect.objectContaining({ page: 1, page_size: 10 }))
    expect(wrapper.text()).toContain('TRON (TRC20)')
    expect(wrapper.text()).toContain('payment.admin.attentionRequired')
    expect(wrapper.text()).toContain('tx-admin-operations')
    expect(wrapper.text()).toContain('5 USDT')
    expect(wrapper.text()).toContain('#12345')
    expect(wrapper.text()).toContain('payment.admin.nodeBlockLag')
    expect(wrapper.text()).toContain('45 USDT')
    expect(wrapper.text()).toContain('payment.admin.signerHealth')
    expect(wrapper.text()).toContain('P1 · SETTLEMENT_BACKLOG')
  })

  it('loads the review-only endpoint and opens the linked order', async () => {
    const wrapper = mount(AdminOnchainOperations, {
      global: { stubs: { Icon: true, Pagination: true } },
    })
    await flushPromises()

    const reviewTab = wrapper.findAll('button').find((button) => button.text() === 'payment.admin.manualReviewQueue')
    expect(reviewTab).toBeTruthy()
    await reviewTab!.trigger('click')
    await flushPromises()

    expect(apiMocks.getOnchainReviews).toHaveBeenCalledWith(expect.objectContaining({ page: 1, page_size: 10 }))
    expect(wrapper.text()).toContain('REVIEW_REQUIRED')

    const orderButton = wrapper.findAll('button').find((button) => button.text().includes('#21'))
    expect(orderButton).toBeTruthy()
    await orderButton!.trigger('click')
    expect(wrapper.emitted('viewOrder')).toEqual([[21]])
  })

  it('shows the independent cold-wallet approval prompt and paused sweeps', async () => {
    apiMocks.getOnchainHealth.mockResolvedValue({
      data: [{
        network: 'tron-mainnet',
        chain_id: 0,
        node_healthy: true,
        cursor_health: 'HEALTHY',
        finalized_height: 12345,
        pending_settlements: 0,
        review_required: 0,
        hot_wallet_address: 'TWallet',
        hot_wallet_balance_raw: '100000000000',
        hot_wallet_warning_threshold_raw: '50000000000',
        hot_wallet_cold_approval_threshold_raw: '100000000000',
        hot_wallet_warning: true,
        cold_transfer_approval_required: true,
        automatic_sweeps_paused: true,
        updated_at: '2026-07-31T10:00:00Z',
      }],
    })

    const wrapper = mount(AdminOnchainOperations, {
      global: { stubs: { Icon: true, Pagination: true } },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('100000 USDT')
    expect(wrapper.text()).toContain('payment.admin.paused')
    expect(wrapper.text()).toContain('payment.admin.coldTransferApprovalRequired')
    expect(wrapper.text()).toContain('payment.admin.attentionRequired')
  })
})
