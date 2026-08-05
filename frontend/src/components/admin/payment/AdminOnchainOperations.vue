<template>
  <section class="border-y border-gray-200 py-4 dark:border-dark-700">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
      <div class="flex items-center gap-2">
        <Icon name="server" size="md" class="text-gray-500" />
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('payment.admin.onchainOperations') }}
        </h2>
      </div>
      <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadAll">
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
      </button>
    </div>

    <div class="mb-4 grid gap-2 lg:grid-cols-2">
      <div
        v-for="item in health"
        :key="item.network"
        class="rounded-md border border-gray-200 px-3 py-2.5 dark:border-dark-600"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <p class="text-sm font-medium text-gray-900 dark:text-white">{{ networkLabel(item.network) }}</p>
            <p class="font-mono text-xs text-gray-500">{{ item.network }} · {{ t('payment.admin.chainId') }} {{ item.chain_id }}</p>
          </div>
          <span :class="healthBadgeClass(item)">
            {{ healthLabel(item) }}
          </span>
        </div>
        <div v-if="item.alerts?.length" class="mt-2 flex flex-wrap gap-1.5">
          <span
            v-for="alert in item.alerts"
            :key="alert.code"
            :class="alertBadgeClass(alert.severity)"
            :title="alert.value && alert.threshold ? `${alert.value} / ${alert.threshold}` : alert.code"
          >
            {{ alert.severity }} · {{ alertLabel(alert.code) }}
          </span>
        </div>
        <div class="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-3 xl:grid-cols-6">
          <div>
            <span class="text-gray-500">{{ t(item.network.startsWith('ethereum-') ? 'payment.admin.ethereumNodeHeights' : 'payment.admin.nodeHeights') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">
              {{ item.network.startsWith('ethereum-') ? `${item.primary_latest_height} / ${item.backup_latest_height}` : `${item.full_node_height} / ${item.solid_height}` }}
            </p>
          </div>
          <div>
            <span class="text-gray-500">{{ t(item.network.startsWith('ethereum-') ? 'payment.admin.finalizedLag' : 'payment.admin.nodeBlockLag') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ item.network.startsWith('ethereum-') ? item.finalized_lag_blocks : item.node_block_lag }}</p>
          </div>
          <div>
            <span class="text-gray-500">{{ t('payment.admin.scanHeight') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ item.finalized_height }}</p>
          </div>
          <div>
            <span class="text-gray-500">{{ t('payment.admin.scanLag') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ item.scan_lag_blocks }} / {{ formatDuration(item.scan_lag_seconds) }}</p>
          </div>
          <div>
            <span class="text-gray-500">{{ t('payment.admin.lastScan') }}</span>
            <p class="text-gray-800 dark:text-gray-200">{{ formatDateTime(item.last_success_at) }}</p>
          </div>
          <div>
            <span class="text-gray-500">{{ t('payment.admin.pendingSettlement') }}</span>
            <p class="text-gray-800 dark:text-gray-200">{{ item.pending_settlements }}</p>
          </div>
          <div>
            <span class="text-gray-500">{{ t('payment.admin.manualReview') }}</span>
            <p :class="item.review_required ? 'font-medium text-amber-600 dark:text-amber-400' : 'text-gray-800 dark:text-gray-200'">
              {{ item.review_required }}
            </p>
          </div>
          <div>
            <span class="text-gray-500">{{ t('payment.admin.reconciliationMismatches') }}</span>
            <p :class="item.reconciliation_mismatches ? 'font-medium text-red-600 dark:text-red-400' : 'text-gray-800 dark:text-gray-200'">
              {{ item.reconciliation_mismatches || 0 }}
            </p>
          </div>
          <div>
            <span class="text-gray-500">{{ t('payment.admin.unsweptBalance') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ formatUSDT(item.unswept_balance_raw || '0') }} USDT</p>
          </div>
          <div>
            <span class="text-gray-500">{{ t('payment.admin.resourceWaitSweeps') }}</span>
            <p class="text-gray-800 dark:text-gray-200">{{ item.resource_wait_sweeps }}</p>
          </div>
          <div v-if="item.network.startsWith('tron-')">
            <span class="text-gray-500">{{ t('payment.admin.tronResources') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ formatTRX(item.trx_balance_sun) }} TRX</p>
            <p class="font-mono text-gray-500">E {{ item.available_energy }} / B {{ item.available_bandwidth }}</p>
          </div>
          <div v-if="item.network.startsWith('tron-')">
            <span class="text-gray-500">{{ t('payment.admin.signerHealth') }}</span>
            <p :class="item.signer_healthy ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'">
              {{ item.signer_healthy ? t('payment.admin.healthy') : t('payment.admin.attentionRequired') }}
            </p>
          </div>
          <div>
            <span class="text-gray-500">{{ t('payment.admin.sweepFailuresRetries') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ item.sweep_failure_count }} / {{ item.sweep_retry_count }}</p>
          </div>
          <div v-if="item.network.startsWith('ethereum-')">
            <span class="text-gray-500">{{ t('payment.admin.ethereumPendingTransactions') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ item.pending_transaction_count }} / {{ formatDuration(item.stuck_transaction_age_seconds) }}</p>
          </div>
          <div v-if="item.network.startsWith('ethereum-')">
            <span class="text-gray-500">{{ t('payment.admin.ethereumNonce') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ item.pending_nonce_count }} / {{ item.nonce_conflict_count }}</p>
          </div>
          <div v-if="item.network.startsWith('ethereum-')">
            <span class="text-gray-500">{{ t('payment.admin.ethereumReplacements') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ item.replacement_transaction_count }}</p>
          </div>
          <div v-if="item.network.startsWith('ethereum-')">
            <span class="text-gray-500">{{ t('payment.admin.ethereumGasSponsor') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ formatETH(item.gas_sponsor_balance_wei || '0') }} ETH</p>
          </div>
          <div v-if="item.network.startsWith('ethereum-')">
            <span class="text-gray-500">{{ t('payment.admin.ethereumGasCost') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">{{ formatETH(item.gas_cost_wei || '0') }} ETH</p>
          </div>
          <div v-if="item.hot_wallet_address">
            <span class="text-gray-500">{{ t('payment.admin.hotWalletBalance') }}</span>
            <p class="font-mono text-gray-800 dark:text-gray-200">
              {{ formatUSDT(item.hot_wallet_balance_raw || '0') }} USDT
            </p>
          </div>
          <div v-if="item.hot_wallet_address">
            <span class="text-gray-500">{{ t('payment.admin.automaticSweeps') }}</span>
            <p :class="item.automatic_sweeps_paused ? 'font-medium text-red-600 dark:text-red-400' : 'text-gray-800 dark:text-gray-200'">
              {{ item.automatic_sweeps_paused ? t('payment.admin.paused') : t('payment.admin.active') }}
            </p>
          </div>
        </div>
        <div
          v-if="item.cold_transfer_approval_required"
          class="mt-2 flex items-start gap-2 rounded border border-red-200 bg-red-50 px-2.5 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300"
        >
          <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" />
          <span>{{ t('payment.admin.coldTransferApprovalRequired', { amount: formatUSDT(item.hot_wallet_cold_approval_threshold_raw || '0') }) }}</span>
        </div>
        <p v-else-if="item.hot_wallet_warning" class="mt-2 text-xs text-amber-600 dark:text-amber-400">
          {{ t('payment.admin.hotWalletWarning', { amount: formatUSDT(item.hot_wallet_warning_threshold_raw || '0') }) }}
        </p>
        <p v-if="item.hot_wallet_error" class="mt-2 break-all text-xs text-red-600 dark:text-red-400">
          {{ item.hot_wallet_error }}
        </p>
        <p v-if="item.resource_error" class="mt-2 break-all text-xs text-red-600 dark:text-red-400">
          {{ item.resource_error }}
        </p>
        <p v-if="item.gas_sponsor_error" class="mt-2 break-all text-xs text-red-600 dark:text-red-400">
          {{ item.gas_sponsor_error }}
        </p>
        <p v-if="item.signer_error" class="mt-2 break-all text-xs text-red-600 dark:text-red-400">
          {{ item.signer_error }}
        </p>
        <p v-if="item.node_error || item.last_error_message" class="mt-2 break-all text-xs text-red-600 dark:text-red-400">
          {{ item.node_error || item.last_error_message }}
        </p>
      </div>
      <p v-if="!healthLoading && health.length === 0" class="text-sm text-gray-500">
        {{ t('payment.admin.noOnchainHealth') }}
      </p>
    </div>

    <div class="mb-3 flex flex-wrap items-center gap-2">
      <div class="inline-flex rounded-md border border-gray-200 p-0.5 dark:border-dark-600">
        <button
          v-for="tab in ledgerTabs"
          :key="tab.value"
          type="button"
          class="rounded px-3 py-1.5 text-xs font-medium transition-colors"
          :class="activeTab === tab.value ? 'bg-gray-900 text-white dark:bg-gray-100 dark:text-gray-900' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-600'"
          @click="setTab(tab.value)"
        >
          {{ tab.label }}
        </button>
      </div>
      <Select v-model="filters.network" :options="networkOptions" class="w-44" @change="resetAndLoad" />
      <Select
        v-if="activeTab === 'deposits'"
        v-model="filters.status"
        :options="statusOptions"
        class="w-44"
        @change="resetAndLoad"
      />
      <input v-model="filters.transaction_id" class="input min-w-48 flex-1" :placeholder="t('payment.admin.transactionId')" @keyup.enter="resetAndLoad" />
      <input v-model="filters.address" class="input min-w-48 flex-1" :placeholder="t('payment.admin.chainAddress')" @keyup.enter="resetAndLoad" />
      <input v-model="filters.order_id" class="input w-32" inputmode="numeric" :placeholder="t('payment.admin.orderIdFilter')" @keyup.enter="resetAndLoad" />
      <button class="btn btn-secondary" :title="t('common.search')" @click="resetAndLoad">
        <Icon name="search" size="sm" />
      </button>
    </div>

    <div class="overflow-x-auto border-t border-gray-200 dark:border-dark-700">
      <table class="min-w-full divide-y divide-gray-200 text-left text-xs dark:divide-dark-700">
        <thead class="text-gray-500 dark:text-gray-400">
          <tr>
            <th class="px-2 py-2 font-medium">{{ t('payment.admin.network') }}</th>
            <th class="px-2 py-2 font-medium">{{ t('payment.admin.transactionLog') }}</th>
            <th class="px-2 py-2 font-medium">{{ t('payment.admin.orderUser') }}</th>
            <th class="px-2 py-2 font-medium">{{ t('payment.orders.amount') }}</th>
            <th class="px-2 py-2 font-medium">{{ t('payment.admin.block') }}</th>
            <th class="px-2 py-2 font-medium">{{ t('payment.orders.status') }}</th>
            <th class="w-10 px-2 py-2"><span class="sr-only">{{ t('common.view') }}</span></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="deposit in deposits" :key="deposit.id" class="align-top">
            <td class="whitespace-nowrap px-2 py-2.5 text-gray-700 dark:text-gray-300">{{ networkLabel(deposit.network) }}</td>
            <td class="max-w-64 px-2 py-2.5">
              <p class="break-all font-mono text-gray-800 dark:text-gray-200">{{ deposit.transaction_id }}</p>
              <p class="mt-0.5 text-gray-500">log {{ deposit.log_index }}</p>
            </td>
            <td class="whitespace-nowrap px-2 py-2.5">
              <button class="font-medium text-blue-600 hover:underline dark:text-blue-400" @click="emit('viewOrder', deposit.payment_order_id)">
                #{{ deposit.payment_order_id }}
              </button>
              <p class="text-gray-500">{{ t('payment.orders.userId') }} #{{ deposit.user_id }}</p>
            </td>
            <td class="whitespace-nowrap px-2 py-2.5 font-mono text-gray-800 dark:text-gray-200">{{ formatUSDT(deposit.amount_raw) }} USDT</td>
            <td class="whitespace-nowrap px-2 py-2.5 text-gray-700 dark:text-gray-300">
              <p>#{{ deposit.block_height }}</p>
              <p class="text-gray-500">{{ formatDateTime(deposit.transaction_time) }}</p>
            </td>
            <td class="px-2 py-2.5">
              <span :class="statusBadgeClass(deposit.status)">{{ statusLabel(deposit.status) }}</span>
              <p v-if="deposit.validation_error" class="mt-1 max-w-48 break-all text-red-600">{{ deposit.validation_error }}</p>
            </td>
            <td class="px-2 py-2.5">
              <button :title="t('common.view')" class="rounded p-1 text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-600" @click="emit('viewOrder', deposit.payment_order_id)">
                <Icon name="eye" size="sm" />
              </button>
            </td>
          </tr>
          <tr v-if="!ledgerLoading && deposits.length === 0">
            <td colspan="7" class="px-2 py-8 text-center text-sm text-gray-500">{{ t('payment.admin.noOnchainDeposits') }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <Pagination
      v-if="pagination.total > 0"
      class="mt-3"
      :page="pagination.page"
      :page-size="pagination.page_size"
      :total="pagination.total"
      @update:page="handlePageChange"
      @update:pageSize="handlePageSizeChange"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { AdminOnchainDeposit, AdminOnchainHealth, AdminOnchainDepositQuery } from '@/api/admin/payment'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import Icon from '@/components/icons/Icon.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'

type LedgerTab = 'deposits' | 'reviews'

const emit = defineEmits<{ (event: 'viewOrder', orderId: number): void }>()
const { t } = useI18n()
const appStore = useAppStore()
const health = ref<AdminOnchainHealth[]>([])
const deposits = ref<AdminOnchainDeposit[]>([])
const healthLoading = ref(false)
const ledgerLoading = ref(false)
const activeTab = ref<LedgerTab>('deposits')
const filters = reactive({ network: '', status: '', transaction_id: '', address: '', order_id: '' })
const pagination = reactive({ page: 1, page_size: 10, total: 0 })
const loading = computed(() => healthLoading.value || ledgerLoading.value)

const ledgerTabs = computed(() => [
  { value: 'deposits' as const, label: t('payment.admin.onchainDeposits') },
  { value: 'reviews' as const, label: t('payment.admin.manualReviewQueue') }
])
const networkOptions = computed(() => [
  { value: '', label: t('payment.admin.allNetworks') },
  { value: 'tron-mainnet', label: 'TRON (TRC20)' },
  { value: 'ethereum-mainnet', label: 'Ethereum (ERC20)' }
])
const statusOptions = computed(() => [
  { value: '', label: t('payment.admin.allStatuses') },
  { value: 'CONFIRMED', label: t('payment.admin.onchainStatus.confirmed') },
  { value: 'CREDIT_PENDING', label: t('payment.admin.onchainStatus.credit_pending') },
  { value: 'CREDITED', label: t('payment.admin.onchainStatus.credited') },
  { value: 'REVIEW_REQUIRED', label: t('payment.admin.onchainStatus.review_required') }
])

function positiveInteger(value: string): number | undefined {
  const trimmed = value.trim()
  if (!/^\d+$/.test(trimmed)) return undefined
  const parsed = Number(trimmed)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined
}

function queryParams(): AdminOnchainDepositQuery {
  return {
    page: pagination.page,
    page_size: pagination.page_size,
    network: filters.network || undefined,
    status: activeTab.value === 'deposits' ? filters.status || undefined : undefined,
    transaction_id: filters.transaction_id.trim() || undefined,
    address: filters.address.trim() || undefined,
    order_id: positiveInteger(filters.order_id)
  }
}

async function loadHealth() {
  healthLoading.value = true
  try {
    const response = await adminPaymentAPI.getOnchainHealth()
    health.value = response.data || []
  } catch (error: unknown) {
    appStore.showError(extractI18nErrorMessage(error, t, 'payment.errors', t('common.error')))
  } finally {
    healthLoading.value = false
  }
}

async function loadLedger() {
  ledgerLoading.value = true
  try {
    const response = activeTab.value === 'reviews'
      ? await adminPaymentAPI.getOnchainReviews(queryParams())
      : await adminPaymentAPI.getOnchainDeposits(queryParams())
    deposits.value = response.data.items || []
    pagination.total = response.data.total || 0
  } catch (error: unknown) {
    appStore.showError(extractI18nErrorMessage(error, t, 'payment.errors', t('common.error')))
  } finally {
    ledgerLoading.value = false
  }
}

function loadAll() {
  void Promise.all([loadHealth(), loadLedger()])
}

function setTab(tab: LedgerTab) {
  activeTab.value = tab
  pagination.page = 1
  void loadLedger()
}

function resetAndLoad() {
  pagination.page = 1
  void loadLedger()
}

function handlePageChange(page: number) {
  pagination.page = page
  void loadLedger()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  void loadLedger()
}

function networkLabel(network: string): string {
  if (network.startsWith('tron-')) return 'TRON (TRC20)'
  if (network.startsWith('ethereum-')) return 'Ethereum (ERC20)'
  return network
}

function healthLabel(item: AdminOnchainHealth): string {
  const networkSpecificHealthy = item.network.startsWith('ethereum-')
    ? item.node_consistent
    : item.signer_healthy && !item.hot_wallet_warning && !item.automatic_sweeps_paused
  return item.node_healthy && item.cursor_health === 'HEALTHY' && networkSpecificHealthy && !item.alerts?.length
    ? t('payment.admin.healthy')
    : t('payment.admin.attentionRequired')
}

function healthBadgeClass(item: AdminOnchainHealth): string {
  const networkSpecificHealthy = item.network.startsWith('ethereum-')
    ? item.node_consistent
    : item.signer_healthy && !item.hot_wallet_warning && !item.automatic_sweeps_paused
  const healthy = item.node_healthy && item.cursor_health === 'HEALTHY' && networkSpecificHealthy && !item.alerts?.length
  return healthy
    ? 'badge bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
    : 'badge bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
}

function alertBadgeClass(severity: string): string {
  if (severity === 'P0') return 'badge bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (severity === 'P1') return 'badge bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  return 'badge bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
}

function alertLabel(code: string): string {
  return t(`payment.admin.onchainAlert.${code.toLowerCase()}`, code)
}

function statusBadgeClass(status: string): string {
  if (status === 'CREDITED') return 'badge bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
  if (status === 'REVIEW_REQUIRED') return 'badge bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  return 'badge bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
}

function statusLabel(status: string): string {
  return t(`payment.admin.onchainStatus.${status.toLowerCase()}`, status)
}

function formatUSDT(raw: string): string {
  const normalized = String(raw || '0').replace(/^0+(?=\d)/, '')
  const padded = normalized.padStart(7, '0')
  const whole = padded.slice(0, -6)
  const fraction = padded.slice(-6).replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : whole
}

function formatDateTime(value?: string): string {
  return value ? formatOrderDateTime(value) : '-'
}

function formatDuration(seconds: number): string {
  const value = Math.max(0, Number(seconds) || 0)
  if (value < 60) return `${value}s`
  if (value < 3600) return `${Math.floor(value / 60)}m`
  return `${Math.floor(value / 3600)}h`
}

function formatTRX(sun: number): string {
  return (Math.max(0, Number(sun) || 0) / 1_000_000).toLocaleString(undefined, { maximumFractionDigits: 6 })
}

function formatETH(raw: string): string {
  const normalized = String(raw || '0').replace(/^0+(?=\d)/, '')
  if (!/^\d+$/.test(normalized)) return '0'
  const padded = normalized.padStart(19, '0')
  const whole = padded.slice(0, -18)
  const fraction = padded.slice(-18).replace(/0+$/, '').slice(0, 6)
  return fraction ? `${whole}.${fraction}` : whole
}

onMounted(loadAll)
</script>
