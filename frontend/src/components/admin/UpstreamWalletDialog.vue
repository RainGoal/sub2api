<template>
  <BaseDialog :show="show" :title="t(wallet?.id ? 'admin.upstreamBalances.editWallet' : 'admin.upstreamBalances.addWallet')" :close-on-escape="!saving" :show-close-button="!saving" @close="emit('close')">
    <form id="upstream-wallet-form" class="space-y-5" @submit.prevent="submit">
      <div>
        <label for="wallet-site" class="input-label">{{ t('admin.upstreamBalances.site') }}</label>
        <select id="wallet-site" v-model="form.site_url" class="input" :disabled="!!wallet?.id || saving" required>
          <option value="" disabled>{{ t('admin.upstreamBalances.chooseSite') }}</option>
          <option v-for="site in availableSites" :key="site.site_url" :value="site.site_url">{{ site.site_url }}</option>
        </select>
      </div>
      <div>
        <label for="wallet-name" class="input-label">{{ t('admin.upstreamBalances.walletName') }}</label>
        <input id="wallet-name" v-model.trim="form.name" class="input" maxlength="100" required :disabled="saving" />
      </div>
      <div class="rounded border border-gray-200 p-3 dark:border-dark-600">
        <label class="flex items-start gap-3 text-sm">
          <input v-model="form.single_account" data-test="single-account" type="checkbox" class="mt-0.5" :disabled="saving || hasOtherWallet" />
          <span>{{ t('admin.upstreamBalances.singleAccount') }}<span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.singleAccountHint') }}</span></span>
        </label>
        <p v-if="hasOtherWallet" class="mt-2 text-xs text-amber-600 dark:text-amber-400">{{ t('admin.upstreamBalances.otherWalletHint') }}</p>
      </div>
      <fieldset v-if="!form.single_account" :disabled="saving">
        <legend class="input-label">{{ t('admin.upstreamBalances.linkedAccounts') }}</legend>
        <div class="max-h-44 space-y-2 overflow-y-auto rounded border border-gray-200 p-3 dark:border-dark-600">
          <label v-for="account in siteAccounts" :key="account.id" class="flex items-center gap-2 text-sm" :class="{ 'opacity-40': assignedAccountIds.has(account.id) }">
            <input v-model="form.account_ids" type="checkbox" :value="account.id" :disabled="assignedAccountIds.has(account.id)" />
            <span class="break-all">{{ account.name }} <span class="text-gray-500">#{{ account.id }}</span></span>
          </label>
          <p v-if="siteAccounts.length === 0" class="text-sm text-gray-500">{{ t('admin.upstreamBalances.noAccounts') }}</p>
        </div>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.accountsHint') }}</p>
      </fieldset>
      <div>
        <label for="wallet-query" class="input-label">{{ t('admin.upstreamBalances.queryAccount') }}</label>
        <select id="wallet-query" v-model="form.query_account_id" class="input" :disabled="saving">
          <option :value="null">{{ t('admin.upstreamBalances.automatic') }}</option>
          <option v-if="form.query_account_id != null && !queryAccounts.some(account => account.id === form.query_account_id)" :value="form.query_account_id">{{ t('admin.upstreamBalances.unavailableQuery', { id: form.query_account_id }) }}</option>
          <option v-for="account in queryAccounts" :key="account.id" :value="account.id">{{ account.name }} #{{ account.id }}</option>
        </select>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.queryHint') }}</p>
      </div>
      <div>
        <label for="wallet-threshold" class="input-label">{{ t('admin.upstreamBalances.threshold') }}</label>
        <input id="wallet-threshold" v-model.number="form.threshold" class="input" type="number" min="0" step="any" required :disabled="saving" />
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.thresholdHint') }}</p>
      </div>
      <div>
        <label for="wallet-recharge" class="input-label">{{ t('admin.upstreamBalances.rechargeURL') }}</label>
        <input id="wallet-recharge" v-model.trim="form.recharge_url" class="input" type="url" placeholder="https://" :disabled="saving" />
      </div>
      <label class="flex items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" :disabled="saving" />{{ t('admin.upstreamBalances.walletEnabled') }}</label>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button form="upstream-wallet-form" type="submit" class="btn btn-primary" :disabled="saving || !form.site_url">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { UpstreamBalanceSite, UpstreamWallet } from '@/api/admin/upstreamBalances'

const props = defineProps<{ show: boolean; saving: boolean; wallet: UpstreamWallet | null; sites: UpstreamBalanceSite[]; wallets: UpstreamWallet[] }>()
const emit = defineEmits<{ close: []; save: [wallet: UpstreamWallet] }>()
const { t } = useI18n()
const blank = (): UpstreamWallet => ({ id: '', name: '', site_url: '', single_account: false, account_ids: [], query_account_id: null, threshold: 20, recharge_url: '', enabled: true })
const form = reactive<UpstreamWallet>(blank())
const error = ref('')
const availableSites = computed(() => {
  const sites = [...props.sites]
  if (props.wallet?.site_url && !sites.some(site => site.site_url === props.wallet?.site_url)) {
    sites.push({ site_url: props.wallet.site_url, accounts: [] })
  }
  return sites
})
const siteAccounts = computed(() => availableSites.value.find(site => site.site_url === form.site_url)?.accounts ?? [])
const otherWallets = computed(() => props.wallets.filter(wallet => wallet.id !== form.id && wallet.site_url === form.site_url))
const hasOtherWallet = computed(() => otherWallets.value.length > 0)
const assignedAccountIds = computed(() => new Set(otherWallets.value.flatMap(wallet => wallet.single_account ? siteAccounts.value.map(account => account.id) : wallet.account_ids)))
const queryAccounts = computed(() => siteAccounts.value.filter(account => form.single_account || form.account_ids.includes(account.id)))

watch(() => props.show, show => {
  if (!show) return
  const wallet = props.wallet
  Object.assign(form, wallet ? { ...wallet, account_ids: [...wallet.account_ids] } : blank())
  error.value = ''
}, { immediate: true })

watch(() => form.site_url, (site, previous) => {
  if (props.wallet?.id || site === previous) return
  form.account_ids = []
  form.query_account_id = null
  form.single_account = false
  if (!form.name || form.name === previous) form.name = site
})

watch(queryAccounts, (accounts, previous) => {
  if (form.query_account_id != null && previous.some(account => account.id === form.query_account_id) && !accounts.some(account => account.id === form.query_account_id)) form.query_account_id = null
})

function submit() {
  if (props.saving) return
  error.value = ''
  if (!form.name.trim() || !form.site_url || (!form.single_account && form.account_ids.length === 0)) {
    error.value = t('admin.upstreamBalances.validationRequired')
    return
  }
  if (!Number.isFinite(form.threshold) || form.threshold < 0) {
    error.value = t('admin.upstreamBalances.validationThreshold')
    return
  }
  if (form.recharge_url) {
    try {
      const url = new URL(form.recharge_url)
      if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error('url')
    } catch {
      error.value = t('admin.upstreamBalances.validationURL')
      return
    }
  }
  emit('save', { ...form, name: form.name.trim(), account_ids: form.single_account ? [] : [...form.account_ids] })
}
</script>
