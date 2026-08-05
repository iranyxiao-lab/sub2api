/**
 * Admin Payment API endpoints
 * Handles payment management operations for administrators
 */

import { apiClient } from '../client'
import type {
  DashboardStats,
  PaymentOrder,
  PaymentChannel,
  SubscriptionPlan,
  ProviderInstance
} from '@/types/payment'
import type { BasePaginationResponse } from '@/types'

/** Admin-facing payment config returned by GET /admin/payment/config */
export interface AdminPaymentConfig {
  enabled: boolean
  min_amount: number
  max_amount: number
  daily_limit: number
  order_timeout_minutes: number
  max_pending_orders: number
  enabled_payment_types: string[]
  balance_disabled: boolean
  balance_recharge_multiplier: number
  subscription_usd_to_cny_rate: number
  recharge_fee_rate: number
  load_balance_strategy: string
  product_name_prefix: string
  product_name_suffix: string
  help_image_url: string
  help_text: string
}

/** Fields accepted by PUT /admin/payment/config (all optional via pointer semantics) */
export interface UpdatePaymentConfigRequest {
  enabled?: boolean
  min_amount?: number
  max_amount?: number
  daily_limit?: number
  order_timeout_minutes?: number
  max_pending_orders?: number
  enabled_payment_types?: string[]
  balance_disabled?: boolean
  balance_recharge_multiplier?: number
  subscription_usd_to_cny_rate?: number
  recharge_fee_rate?: number
  load_balance_strategy?: string
  product_name_prefix?: string
  product_name_suffix?: string
  help_image_url?: string
  help_text?: string
}

export interface RefundResult {
  success: boolean
  warning?: string
  require_force?: boolean
  manual_review_required?: boolean
  refund_mode?: string
  balance_deducted?: number
  subscription_days_deducted?: number
}

export interface AdminPaymentAuditLog {
  id: number
  action: string
  detail: string | null
  operator: string | null
  created_at: string
}

export interface AdminOnchainHealth {
  network: string
  chain_id: number
  node_healthy: boolean
  node_error?: string
  node_checked_at?: string
  primary_latest_height: number
  backup_latest_height: number
  primary_finalized_height: number
  backup_finalized_height: number
  common_finalized_height: number
  finalized_lag_blocks: number
  node_consistent: boolean
  full_node_height: number
  solid_height: number
  node_block_lag: number
  cursor_health: string
  finalized_height: number
  scan_lag_blocks: number
  scan_lag_seconds: number
  finalized_hash?: string
  lease_owner?: string
  lease_until?: string
  last_success_at?: string
  last_error_code?: string
  last_error_message?: string
  pending_settlements: number
  review_required: number
  reconciliation_mismatches?: number
  unswept_balance_raw: string
  rpc_error_count: number
  pending_nonce_count: number
  nonce_conflict_count: number
  pending_transaction_count: number
  stuck_transaction_age_seconds: number
  replacement_transaction_count: number
  gas_cost_wei: string
  gas_sponsor_address?: string
  gas_sponsor_balance_wei?: string
  gas_sponsor_error?: string
  gas_sponsor_checked_at?: string
  resource_wait_sweeps: number
  sweep_retry_count: number
  sweep_max_retry_count: number
  sweep_failure_count: number
  trx_balance_sun: number
  available_energy: number
  available_bandwidth: number
  resource_error?: string
  resource_checked_at?: string
  signer_healthy: boolean
  signer_error?: string
  signer_checked_at?: string
  alerts: AdminOnchainAlert[]
  hot_wallet_address?: string
  hot_wallet_balance_raw?: string
  hot_wallet_warning_threshold_raw?: string
  hot_wallet_cold_approval_threshold_raw?: string
  hot_wallet_warning: boolean
  cold_transfer_approval_required: boolean
  automatic_sweeps_paused: boolean
  hot_wallet_error?: string
  hot_wallet_checked_at?: string
  updated_at: string
}

export interface AdminOnchainAlert {
  code: string
  severity: string
  value?: string
  threshold?: string
}

export interface AdminOnchainDeposit {
  id: number
  intent_id: number
  payment_order_id: number
  user_id: number
  network: string
  chain_id: number
  transaction_id: string
  log_index: number
  transaction_index: number
  block_height: number
  block_hash: string
  token_contract: string
  from_address: string
  to_address: string
  amount_raw: string
  transaction_time: string
  receipt_success: boolean
  finalized: boolean
  status: string
  validation_error?: string
  credit_audit_ref?: string
  credited_at?: string
  created_at: string
  updated_at: string
}

export interface AdminOnchainIntent {
  id: number
  payment_order_id: number
  user_id: number
  network: string
  chain_id: number
  token_contract: string
  deposit_address: string
  derivation_index: number
  expected_amount_raw: string
  received_amount_raw: string
  credited_amount_raw: string
  overpaid_amount_raw: string
  config_version: string
  status: string
  settlement_attempts: number
  next_settlement_at?: string
  last_error_code?: string
  last_error_message?: string
  settled_at?: string
  created_at: string
  updated_at: string
}

export interface AdminWalletSweep {
  id: number
  intent_id: number
  network: string
  chain_id: number
  source_address: string
  destination_address: string
  balance_snapshot_raw: string
  amount_raw: string
  transaction_id?: string
  nonce?: number
  fee_raw: string
  energy_used: number
  bandwidth_used: number
  status: string
  retry_count: number
  failure_code?: string
  failure_reason?: string
  finalized_block_height?: number
  finalized_block_hash?: string
  finalized_at?: string
  created_at: string
  updated_at: string
}

export interface AdminEthereumGasFunding {
  id: number
  intent_id: number
  chain_id: number
  sponsor_address: string
  target_address: string
  derivation_index: number
  amount_wei: string
  nonce: number
  gas_limit: number
  transaction_hash?: string
  status: string
  finalized: boolean
  finalized_block_height?: number
  actual_fee_wei?: string
  failure_code?: string
  failure_reason?: string
  finalized_at?: string
  created_at: string
  updated_at: string
}

export interface AdminOnchainOrderTrace {
  intent: AdminOnchainIntent
  deposits: AdminOnchainDeposit[]
  balance_audits: AdminOnchainBalanceAudit[]
  sweeps: AdminWalletSweep[]
  gas_fundings: AdminEthereumGasFunding[]
  nonce_states: AdminEthereumNonceState[]
}

export interface AdminEthereumNonceState {
  id: number
  chain_id: number
  sender_address: string
  next_nonce: number
  observed_pending_nonce: number
  status: string
  lease_owner?: string
  lease_until?: string
  last_reconciled_at?: string
  last_error_code?: string
  last_error_message?: string
  created_at: string
  updated_at: string
}

export interface AdminOnchainBalanceAudit {
  id: number
  action: string
  detail: string
  operator: string
  created_at: string
}

export interface AdminOrderDetailResponse {
  order: PaymentOrder
  auditLogs: AdminPaymentAuditLog[]
  onchain_trace: AdminOnchainOrderTrace | null
}

export interface AdminOnchainDepositQuery {
  page?: number
  page_size?: number
  network?: string
  status?: string
  transaction_id?: string
  address?: string
  order_id?: number
  user_id?: number
  log_index?: number
}

export const adminPaymentAPI = {
  // ==================== Config ====================

  /** Get payment configuration (admin view) */
  getConfig() {
    return apiClient.get<AdminPaymentConfig>('/admin/payment/config')
  },

  /** Update payment configuration */
  updateConfig(data: UpdatePaymentConfigRequest) {
    return apiClient.put('/admin/payment/config', data)
  },

  // ==================== Dashboard ====================

  /** Get payment dashboard statistics */
  getDashboard(days?: number) {
    return apiClient.get<DashboardStats>('/admin/payment/dashboard', {
      params: days ? { days } : undefined
    })
  },

  // ==================== Orders ====================

  /** Get all orders (paginated, with filters) */
  getOrders(params?: {
    page?: number
    page_size?: number
    status?: string
    payment_type?: string
    user_id?: number
    keyword?: string
    start_date?: string
    end_date?: string
    order_type?: string
  }) {
    return apiClient.get<BasePaginationResponse<PaymentOrder>>('/admin/payment/orders', { params })
  },

  /** Get a specific order by ID */
  getOrder(id: number) {
    return apiClient.get<AdminOrderDetailResponse>(`/admin/payment/orders/${id}`)
  },

  /** Get node and durable scanner health for on-chain networks. */
  getOnchainHealth() {
    return apiClient.get<AdminOnchainHealth[]>('/admin/payment/onchain/health')
  },

  /** Query the durable on-chain deposit ledger. */
  getOnchainDeposits(params?: AdminOnchainDepositQuery) {
    return apiClient.get<BasePaginationResponse<AdminOnchainDeposit>>('/admin/payment/onchain/deposits', { params })
  },

  /** Query deposits requiring manual review. */
  getOnchainReviews(params?: Omit<AdminOnchainDepositQuery, 'status'>) {
    return apiClient.get<BasePaginationResponse<AdminOnchainDeposit>>('/admin/payment/onchain/reviews', { params })
  },

  /** Cancel an order (admin) */
  cancelOrder(id: number) {
    return apiClient.post(`/admin/payment/orders/${id}/cancel`)
  },

  /** Retry recharge for a failed order */
  retryRecharge(id: number) {
    return apiClient.post(`/admin/payment/orders/${id}/retry`)
  },

  /** Process a refund */
  refundOrder(id: number, data: { amount: number; reason: string; deduct_balance?: boolean; force?: boolean }) {
    return apiClient.post<RefundResult>(`/admin/payment/orders/${id}/refund`, data)
  },

  /** Query and finalize a pending refund */
  queryRefund(id: number) {
    return apiClient.post<RefundResult>(`/admin/payment/orders/${id}/refund/query`)
  },

  // ==================== Channels ====================

  /** Get all payment channels */
  getChannels() {
    return apiClient.get<PaymentChannel[]>('/admin/payment/channels')
  },

  /** Create a payment channel */
  createChannel(data: Partial<PaymentChannel>) {
    return apiClient.post<PaymentChannel>('/admin/payment/channels', data)
  },

  /** Update a payment channel */
  updateChannel(id: number, data: Partial<PaymentChannel>) {
    return apiClient.put<PaymentChannel>(`/admin/payment/channels/${id}`, data)
  },

  /** Delete a payment channel */
  deleteChannel(id: number) {
    return apiClient.delete(`/admin/payment/channels/${id}`)
  },

  // ==================== Subscription Plans ====================

  /** Get all subscription plans */
  getPlans() {
    return apiClient.get<SubscriptionPlan[]>('/admin/payment/plans')
  },

  /** Create a subscription plan */
  createPlan(data: Record<string, unknown>) {
    return apiClient.post<SubscriptionPlan>('/admin/payment/plans', data)
  },

  /** Update a subscription plan */
  updatePlan(id: number, data: Record<string, unknown>) {
    return apiClient.put<SubscriptionPlan>(`/admin/payment/plans/${id}`, data)
  },

  /** Delete a subscription plan */
  deletePlan(id: number) {
    return apiClient.delete(`/admin/payment/plans/${id}`)
  },

  // ==================== Provider Instances ====================

  /** Get all provider instances */
  getProviders() {
    return apiClient.get<ProviderInstance[]>('/admin/payment/providers')
  },

  /** Create a provider instance */
  createProvider(data: Partial<ProviderInstance>) {
    return apiClient.post<ProviderInstance>('/admin/payment/providers', data)
  },

  /** Update a provider instance */
  updateProvider(id: number, data: Partial<ProviderInstance>) {
    return apiClient.put<ProviderInstance>(`/admin/payment/providers/${id}`, data)
  },

  /** Delete a provider instance */
  deleteProvider(id: number) {
    return apiClient.delete(`/admin/payment/providers/${id}`)
  }
}

export default adminPaymentAPI
