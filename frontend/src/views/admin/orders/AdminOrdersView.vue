<template>
  <AppLayout>
    <div class="space-y-4">
      <AdminOnchainOperations @view-order="showOrderDetailById" />

      <!-- Filters -->
      <div class="card p-4">
        <div class="flex flex-wrap items-center gap-3">
          <div class="flex-1 sm:max-w-64">
            <input v-model="orderSearch" type="text" :placeholder="t('payment.admin.searchOrders')" class="input" @input="debounceLoadOrders" />
          </div>
          <Select v-model="orderFilters.status" :options="statusFilterOptions" class="w-36" @change="loadOrders" />
          <Select v-model="orderFilters.payment_type" :options="paymentTypeFilterOptions" class="w-40" @change="loadOrders" />
          <Select v-model="orderFilters.order_type" :options="orderTypeFilterOptions" class="w-36" @change="loadOrders" />
          <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
            <button @click="loadOrders" :disabled="ordersLoading" class="btn btn-secondary" :title="t('common.refresh')">
              <Icon name="refresh" size="md" :class="ordersLoading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
      </div>

      <!-- Table -->
      <OrderTable :orders="orders" :loading="ordersLoading" show-user>
        <template #actions="{ row }">
          <div class="flex items-center gap-1">
            <button @click="showOrderDetail(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-dark-600">
              <Icon name="eye" size="sm" />
              {{ t('common.view') }}
            </button>
            <button v-if="row.status === 'PENDING'" @click="handleCancelOrder(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-yellow-600 hover:bg-yellow-50 dark:text-yellow-400 dark:hover:bg-yellow-900/20">
              <Icon name="x" size="sm" />
              {{ t('payment.orders.cancel') }}
            </button>
            <button v-if="row.status === 'FAILED'" @click="handleRetryOrder(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20">
              <Icon name="refresh" size="sm" />
              {{ t('payment.admin.retry') }}
            </button>
            <template v-if="row.status === 'REFUND_REQUESTED'">
              <span v-if="row.refund_amount" class="rounded-full bg-purple-100 px-1.5 py-0.5 text-xs font-medium text-purple-700 dark:bg-purple-900/30 dark:text-purple-300">{{ creditedAmountSymbol }}{{ row.refund_amount.toFixed(2) }}</span>
              <span v-if="isOnchainPayment(row.payment_type)" class="inline-flex items-center gap-1 text-xs font-medium text-amber-600 dark:text-amber-400">
                <Icon name="ban" size="sm" />
                {{ t('payment.admin.manualReviewPending') }}
              </span>
              <button v-else @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
                <Icon name="check" size="sm" />
                {{ t('payment.admin.approveRefund') }}
              </button>
            </template>
            <button v-else-if="row.status === 'REFUND_FAILED'" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
              <Icon name="refresh" size="sm" />
              {{ t('payment.admin.retryRefund') }}
            </button>
            <button v-else-if="row.status === 'REFUND_PENDING'" :disabled="refundQueryingIds.has(row.id)" @click="handleQueryRefund(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-orange-600 hover:bg-orange-50 disabled:opacity-60 dark:text-orange-400 dark:hover:bg-orange-900/20">
              <Icon name="refresh" size="sm" :class="refundQueryingIds.has(row.id) ? 'animate-spin' : ''" />
              {{ t('payment.admin.queryRefundStatus') }}
            </button>
            <button v-else-if="row.status === 'COMPLETED' || row.status === 'PARTIALLY_REFUNDED'" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20">
              <Icon name="dollar" size="sm" />
              {{ t('payment.admin.refund') }}
            </button>
          </div>
        </template>
      </OrderTable>
      <Pagination v-if="orderPagination.total > 0" :page="orderPagination.page" :total="orderPagination.total" :page-size="orderPagination.page_size" @update:page="handleOrderPageChange" @update:pageSize="handleOrderPageSizeChange" />
    </div>

    <!-- Order Detail Dialog -->
    <BaseDialog :show="showDetailDialog" :title="t('payment.admin.orderDetail')" width="wide" @close="showDetailDialog = false">
      <div v-if="selectedOrder" class="space-y-4">
        <div class="grid grid-cols-2 gap-4">
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</p><p class="font-mono text-sm font-medium text-gray-900 dark:text-white">#{{ selectedOrder.id }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</p><p class="text-sm font-medium text-gray-900 dark:text-white">{{ selectedOrder.out_trade_no }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.status') }}</p><OrderStatusBadge :status="selectedOrder.status" /></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</p><p class="text-sm font-medium text-gray-900 dark:text-white">{{ creditedAmountSymbol }}{{ selectedOrder.amount.toFixed(2) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</p><p class="text-sm font-medium text-gray-900 dark:text-white">{{ paymentAmountSymbol(selectedOrder) }}{{ selectedOrder.pay_amount.toFixed(2) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.paymentMethod') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ t('payment.methods.' + selectedOrder.payment_type, selectedOrder.payment_type) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.feeRate') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.fee_rate }}%</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.createdAt') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.created_at) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.expiresAt') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.expires_at) }}</p></div>
          <div v-if="selectedOrder.paid_at"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.paidAt') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.paid_at) }}</p></div>
          <div v-if="selectedOrder.refund_amount"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundAmount') }}</p><p class="text-sm font-medium text-red-600 dark:text-red-400">{{ creditedAmountSymbol }}{{ selectedOrder.refund_amount.toFixed(2) }}</p></div>
          <div v-if="selectedOrder.refund_reason" class="col-span-2"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundReason') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.refund_reason }}</p></div>
          <!-- Refund request info -->
          <div v-if="selectedOrder.refund_requested_at" class="col-span-2 border-t border-gray-200 pt-3 dark:border-dark-600">
            <p class="mb-2 text-xs font-medium text-purple-600 dark:text-purple-400">{{ t('payment.admin.refundRequestInfo') }}</p>
            <div class="grid grid-cols-2 gap-4">
              <div>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestedAt') }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.refund_requested_at) }}</p>
              </div>
              <div>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestedBy') }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">#{{ selectedOrder.refund_requested_by }}</p>
              </div>
              <div class="col-span-2">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestReason') }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.refund_request_reason }}</p>
              </div>
            </div>
          </div>
        </div>
        <!-- On-chain fund trace -->
        <div v-if="selectedOnchainTrace" class="border-t border-gray-200 pt-4 dark:border-dark-600">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
            <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.fundTrace') }}</p>
            <span class="badge bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300">
              {{ networkLabel(selectedOnchainTrace.intent.network) }}
            </span>
          </div>
          <div class="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <p class="text-xs text-gray-500">{{ t('payment.admin.intentStatus') }}</p>
              <p class="font-medium text-gray-900 dark:text-white">{{ selectedOnchainTrace.intent.status }}</p>
            </div>
            <div>
              <p class="text-xs text-gray-500">{{ t('payment.admin.expectedAmount') }}</p>
              <p class="font-mono text-gray-900 dark:text-white">{{ formatUSDT(selectedOnchainTrace.intent.expected_amount_raw) }} USDT</p>
            </div>
            <div>
              <p class="text-xs text-gray-500">{{ t('payment.admin.receivedAmount') }}</p>
              <p class="font-mono text-gray-900 dark:text-white">{{ formatUSDT(selectedOnchainTrace.intent.received_amount_raw) }} USDT</p>
            </div>
            <div>
              <p class="text-xs text-gray-500">{{ t('payment.admin.creditedOnchainAmount') }}</p>
              <p class="font-mono text-gray-900 dark:text-white">{{ formatUSDT(selectedOnchainTrace.intent.credited_amount_raw) }} USDT</p>
            </div>
            <div class="sm:col-span-2 lg:col-span-4">
              <p class="text-xs text-gray-500">{{ t('payment.admin.depositAddress') }}</p>
              <p class="break-all font-mono text-xs text-gray-900 dark:text-white">{{ selectedOnchainTrace.intent.deposit_address }}</p>
            </div>
            <div class="sm:col-span-2 lg:col-span-4">
              <p class="text-xs text-gray-500">{{ t('payment.admin.tokenContract') }}</p>
              <p class="break-all font-mono text-xs text-gray-700 dark:text-gray-300">{{ selectedOnchainTrace.intent.token_contract }}</p>
            </div>
          </div>
          <p v-if="selectedOnchainTrace.intent.last_error_message" class="mt-3 break-all text-xs text-red-600 dark:text-red-400">
            {{ selectedOnchainTrace.intent.last_error_code }}: {{ selectedOnchainTrace.intent.last_error_message }}
          </p>

          <div class="mt-4 overflow-x-auto">
            <p class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.onchainDeposits') }}</p>
            <table class="min-w-full divide-y divide-gray-200 text-left text-xs dark:divide-dark-700">
              <thead class="text-gray-500">
                <tr>
                  <th class="px-2 py-2 font-medium">{{ t('payment.admin.transactionLog') }}</th>
                  <th class="px-2 py-2 font-medium">{{ t('payment.orders.amount') }}</th>
                  <th class="px-2 py-2 font-medium">{{ t('payment.admin.block') }}</th>
                  <th class="px-2 py-2 font-medium">{{ t('payment.orders.status') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="deposit in selectedOnchainTrace.deposits" :key="deposit.id" class="align-top">
                  <td class="max-w-80 px-2 py-2">
                    <p class="break-all font-mono text-gray-800 dark:text-gray-200">{{ deposit.transaction_id }}</p>
                    <p class="text-gray-500">log {{ deposit.log_index }}</p>
                  </td>
                  <td class="whitespace-nowrap px-2 py-2 font-mono">{{ formatUSDT(deposit.amount_raw) }} USDT</td>
                  <td class="whitespace-nowrap px-2 py-2">#{{ deposit.block_height }}</td>
                  <td class="px-2 py-2">
                    <p>{{ deposit.status }}</p>
                    <p class="text-gray-500">{{ t('payment.admin.receipt') }}: {{ deposit.receipt_success ? t('payment.admin.success') : t('payment.admin.failed') }}</p>
                  </td>
                </tr>
                <tr v-if="selectedOnchainTrace.deposits.length === 0">
                  <td colspan="4" class="px-2 py-4 text-center text-gray-500">{{ t('payment.admin.noOnchainDeposits') }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-if="selectedOnchainTrace.sweeps.length > 0" class="mt-4">
            <p class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.walletSweeps') }}</p>
            <div class="space-y-2">
              <div v-for="sweep in selectedOnchainTrace.sweeps" :key="sweep.id" class="rounded-md border border-gray-200 px-3 py-2 text-xs dark:border-dark-600">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <span class="font-medium text-gray-800 dark:text-gray-200">{{ formatUSDT(sweep.amount_raw) }} USDT</span>
                  <span>{{ sweep.status }}</span>
                </div>
                <p class="mt-1 break-all font-mono text-gray-500">{{ sweep.transaction_id || '-' }}</p>
                <p v-if="sweep.failure_reason" class="mt-1 break-all text-red-600">{{ sweep.failure_code }}: {{ sweep.failure_reason }}</p>
              </div>
            </div>
          </div>

          <div v-if="selectedOnchainTrace.gas_fundings.length > 0" class="mt-4">
            <p class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.gasFundings') }}</p>
            <div class="space-y-2">
              <div v-for="funding in selectedOnchainTrace.gas_fundings" :key="funding.id" class="rounded-md border border-gray-200 px-3 py-2 text-xs dark:border-dark-600">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <span class="font-mono text-gray-800 dark:text-gray-200">{{ funding.amount_wei }} wei</span>
                  <span>{{ funding.status }}</span>
                </div>
                <p class="mt-1 break-all font-mono text-gray-500">{{ funding.transaction_hash || '-' }}</p>
                <p v-if="funding.failure_reason" class="mt-1 break-all text-red-600">{{ funding.failure_code }}: {{ funding.failure_reason }}</p>
              </div>
            </div>
          </div>

          <div v-if="selectedOnchainTrace.nonce_states.length > 0" class="mt-4">
            <p class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.nonceStates') }}</p>
            <div class="space-y-2">
              <div v-for="nonce in selectedOnchainTrace.nonce_states" :key="nonce.id" class="rounded-md border border-gray-200 px-3 py-2 text-xs dark:border-dark-600">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <span class="break-all font-mono text-gray-800 dark:text-gray-200">{{ nonce.sender_address }}</span>
                  <span>{{ nonce.status }}</span>
                </div>
                <p class="mt-1 font-mono text-gray-500">{{ t('payment.admin.nextObservedNonce') }}: {{ nonce.next_nonce }} / {{ nonce.observed_pending_nonce }}</p>
                <p v-if="nonce.last_error_message" class="mt-1 break-all text-red-600">{{ nonce.last_error_code }}: {{ nonce.last_error_message }}</p>
              </div>
            </div>
          </div>
        </div>
        <!-- Audit Logs -->
        <div v-if="orderAuditLogs.length > 0" class="border-t border-gray-200 pt-4 dark:border-dark-600">
          <p class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.auditLogs') }}</p>
          <div class="max-h-48 space-y-2 overflow-y-auto">
            <div v-for="log in orderAuditLogs" :key="log.id" class="rounded-lg border border-gray-100 bg-gray-50 p-2.5 dark:border-dark-600 dark:bg-dark-800">
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-gray-700 dark:text-gray-300">{{ log.action }}</span>
                <span class="text-xs text-gray-400">{{ formatDateTime(log.created_at) }}</span>
              </div>
              <div v-if="log.detail" class="mt-1 break-all text-xs text-gray-500 dark:text-gray-400">{{ log.detail }}</div>
              <div v-if="log.operator" class="mt-1 text-xs text-gray-400">{{ t('payment.admin.operator') }}: {{ log.operator }}</div>
            </div>
          </div>
        </div>
      </div>
    </BaseDialog>

    <AdminRefundDialog :show="showRefundDialog" :order="selectedOrder" :submitting="refundSubmitting" :require-force="refundRequireForce" :warning="refundWarning" @confirm="handleRefund" @cancel="closeRefundDialog" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { AdminOnchainOrderTrace, AdminPaymentAuditLog } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import type { PaymentOrder } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import AdminRefundDialog from '@/components/admin/payment/AdminRefundDialog.vue'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import OrderTable from '@/components/payment/OrderTable.vue'
import AdminOnchainOperations from '@/components/admin/payment/AdminOnchainOperations.vue'
import { currencySymbol } from '@/components/payment/currency'

const { t } = useI18n()
const appStore = useAppStore()

const ordersLoading = ref(false)
const orders = ref<PaymentOrder[]>([])
const orderSearch = ref('')
const orderFilters = reactive({ status: '', payment_type: '', order_type: '' })
const orderPagination = reactive({ page: 1, page_size: 20, total: 0 })
const selectedOrder = ref<PaymentOrder | null>(null)
const showDetailDialog = ref(false)
const showRefundDialog = ref(false)
const refundSubmitting = ref(false)
const refundRequireForce = ref(false)
const refundWarning = ref('')
const refundQueryingIds = ref(new Set<number>())
const orderAuditLogs = ref<AdminPaymentAuditLog[]>([])
const selectedOnchainTrace = ref<AdminOnchainOrderTrace | null>(null)
const creditedAmountSymbol = currencySymbol('USD')

function paymentAmountSymbol(order: PaymentOrder | null | undefined): string {
  return currencySymbol(order?.currency)
}

let debounceTimer: ReturnType<typeof setTimeout> | null = null
function debounceLoadOrders() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => loadOrders(), 300)
}

async function loadOrders() {
  ordersLoading.value = true
  try {
    const res = await adminPaymentAPI.getOrders({
      page: orderPagination.page, page_size: orderPagination.page_size,
      keyword: orderSearch.value || undefined, status: orderFilters.status || undefined,
      payment_type: orderFilters.payment_type || undefined, order_type: orderFilters.order_type || undefined,
    })
    orders.value = res.data.items || []
    orderPagination.total = res.data.total || 0
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally { ordersLoading.value = false }
}

function handleOrderPageChange(page: number) { orderPagination.page = page; loadOrders() }
function handleOrderPageSizeChange(size: number) { orderPagination.page_size = size; orderPagination.page = 1; loadOrders() }

const statusFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allStatuses') },
  { value: 'PENDING', label: t('payment.status.pending') },
  { value: 'PAID', label: t('payment.status.paid') },
  { value: 'COMPLETED', label: t('payment.status.completed') },
  { value: 'EXPIRED', label: t('payment.status.expired') },
  { value: 'CANCELLED', label: t('payment.status.cancelled') },
  { value: 'FAILED', label: t('payment.status.failed') },
  { value: 'REFUNDED', label: t('payment.status.refunded') },
  { value: 'REFUND_REQUESTED', label: t('payment.status.refund_requested') },
  { value: 'REFUND_PENDING', label: t('payment.status.refund_pending') },
  { value: 'REFUND_FAILED', label: t('payment.status.refund_failed') },
])

const paymentTypeFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allPaymentTypes') },
  { value: 'alipay', label: t('payment.methods.alipay') },
  { value: 'wxpay', label: t('payment.methods.wxpay') },
  { value: 'stripe', label: t('payment.methods.stripe') },
  { value: 'airwallex', label: t('payment.methods.airwallex') },
  { value: 'usdt_trc20', label: t('payment.methods.usdt_trc20') },
  { value: 'usdt_erc20', label: t('payment.methods.usdt_erc20') },
])

const orderTypeFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allOrderTypes') },
  { value: 'balance', label: t('payment.admin.balanceOrder') },
  { value: 'subscription', label: t('payment.admin.subscriptionOrder') },
])

async function showOrderDetail(order: PaymentOrder) {
  selectedOrder.value = order
  orderAuditLogs.value = []
  selectedOnchainTrace.value = null
  showDetailDialog.value = true
  await loadOrderDetail(order.id)
}

async function showOrderDetailById(orderId: number) {
  selectedOrder.value = null
  orderAuditLogs.value = []
  selectedOnchainTrace.value = null
  showDetailDialog.value = true
  await loadOrderDetail(orderId)
}

async function loadOrderDetail(orderId: number) {
  try {
    const res = await adminPaymentAPI.getOrder(orderId)
    selectedOrder.value = res.data.order
    orderAuditLogs.value = res.data.auditLogs || []
    selectedOnchainTrace.value = res.data.onchain_trace
  } catch (_err: unknown) { /* keep cached order data */ }
}

async function handleCancelOrder(order: PaymentOrder) {
  try { await adminPaymentAPI.cancelOrder(order.id); appStore.showSuccess(t('payment.admin.orderCancelled')); loadOrders() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

async function handleRetryOrder(order: PaymentOrder) {
  try { await adminPaymentAPI.retryRecharge(order.id); appStore.showSuccess(t('payment.admin.retrySuccess')); loadOrders() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

function openRefundDialog(order: PaymentOrder) {
  selectedOrder.value = order
  refundRequireForce.value = false
  refundWarning.value = ''
  showRefundDialog.value = true
}

function closeRefundDialog() {
  showRefundDialog.value = false
  refundRequireForce.value = false
  refundWarning.value = ''
}

function isRefundPendingWarning(warning: string | undefined): boolean {
  return /pending|处理中|待/.test(String(warning || '').toLowerCase())
}

async function handleRefund(data: { amount: number; reason: string; deduct_balance: boolean; force: boolean }) {
  if (!selectedOrder.value) return
  refundSubmitting.value = true
  try {
    const res = await adminPaymentAPI.refundOrder(selectedOrder.value.id, { amount: data.amount, reason: data.reason, deduct_balance: data.deduct_balance, force: data.force })
    if (res.data.manual_review_required) {
      appStore.showSuccess(t('payment.admin.refundReviewCreated'))
      showRefundDialog.value = false
      loadOrders()
      return
    }
    if (res.data.success) {
      appStore.showSuccess(t('payment.admin.refundSuccess'))
      closeRefundDialog()
      loadOrders()
      return
    }
    if (isRefundPendingWarning(res.data.warning)) {
      appStore.showSuccess(t('payment.admin.refundPending'))
      closeRefundDialog()
      loadOrders()
      return
    }
    if (res.data.require_force) {
      // Backend needs an explicit force confirmation (e.g. the user spent their
      // balance after requesting the refund). Keep the dialog open and surface
      // the force checkbox instead of dropping the admin back to the list.
      refundRequireForce.value = true
      refundWarning.value = res.data.warning || ''
      return
    }
    appStore.showError(res.data.warning || t('common.error'))
  } catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
  finally { refundSubmitting.value = false }
}

async function handleQueryRefund(order: PaymentOrder) {
  refundQueryingIds.value = new Set(refundQueryingIds.value).add(order.id)
  try {
    const res = await adminPaymentAPI.queryRefund(order.id)
    if (res.data.success) {
      appStore.showSuccess(t('payment.admin.refundSuccess'))
    } else if (isRefundPendingWarning(res.data.warning)) {
      appStore.showSuccess(t('payment.admin.refundPending'))
    } else {
      appStore.showError(res.data.warning || t('common.error'))
    }
    loadOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    const next = new Set(refundQueryingIds.value)
    next.delete(order.id)
    refundQueryingIds.value = next
  }
}

function formatDateTime(dateStr: string): string { return formatOrderDateTime(dateStr) }

function networkLabel(network: string): string {
  if (network.startsWith('tron-')) return 'TRON (TRC20)'
  if (network.startsWith('ethereum-')) return 'Ethereum (ERC20)'
  return network
}

function formatUSDT(raw: string): string {
  const normalized = String(raw || '0').replace(/^0+(?=\d)/, '')
  const padded = normalized.padStart(7, '0')
  const whole = padded.slice(0, -6)
  const fraction = padded.slice(-6).replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : whole
}

function isOnchainPayment(paymentType: string): boolean {
  return paymentType === 'usdt_trc20' || paymentType === 'usdt_erc20'
}

onMounted(() => loadOrders())
</script>
