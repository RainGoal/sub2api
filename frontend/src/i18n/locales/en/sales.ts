export default {
  title: 'Sales partners', description: 'Manage referral domains, customer ownership and usage profit sharing.', settings: 'Sales settings', enabled: 'Enable sales program',
  mainUrl: 'Main website URL', mainUrlHint: 'Use the HTTPS origin. Registration, sign-in and payment stay on the main website.',
  addPartner: 'Add partner', editPartner: 'Edit partner', partners: 'Partners', customers: 'Customers', ledger: 'Commission ledger', settlements: 'Settlements',
  name: 'Partner name', userId: 'Platform user ID', userIdHint: 'Partners sign in with an existing platform account. The linked account cannot be changed.',
  code: 'Referral code', hostname: 'Referral domain', hostnameHint: 'Hostname only, without scheme or path. Configure DNS and HTTPS redirects separately.',
  commission_rate: 'Commission rate', promotion_enabled: 'Accept new customers', accrual_enabled: 'Accrue commissions', payout_frozen: 'Freeze payouts',
  ruleHint: 'Rate and accrual changes apply to future usage immediately. Historical entries remain unchanged.', active: 'Enabled', inactive: 'Disabled', frozen: 'Frozen',
  view: 'View customers and accounts', back: 'Back to partners', search: 'Search name, code or domain', referral: 'Main website referral link', domain: 'Referral domain',
  referralHint: 'Redirect the referral domain to this link. Ownership is saved at registration; payment callbacks stay on the main domain.',
  revenue: 'Usage revenue', cost: 'Agreed cost', profit: 'Settlement gross profit', commission: 'Commission', customer_count: 'Total customers',
  unsettled_commission: 'Not yet in a settlement', pending_payout: 'Awaiting payment', paid_commission: 'Paid', pending: '{count} usage events pending',
  scope: 'Pay-as-you-go balance usage. Revenue equals balance deductions. All amounts are USD; gifts and discounts are borne by the platform.',
  dateHint: 'Date filters use UTC. Customer count and payout balances are lifetime totals.', start: 'Start date', end: 'End date', apply: 'Apply filters',
  empty: 'No records yet', emptyHint: 'Records appear after referred customers register and use the platform.', retry: 'Reload', loadError: 'Could not load sales data. Please retry.',
  created_at: 'Registered at', occurred_at: 'Occurred at', email: 'Customer email', customer_user_id: 'Customer ID', kind: 'Type',
  usage: 'Usage', adjustment: 'Adjustment', carry: 'Loss carryforward', note: 'Note', settlement_id: 'Settlement ID', month: 'Settlement month (UTC)',
  net_commission: 'Net commission', payout_amount: 'Amount payable', carry_amount: 'Carried forward', status: 'Status', draft: 'Awaiting review', confirmed: 'Awaiting payment', paid: 'Paid',
  payment_reference: 'Payment reference', createSettlement: 'Create settlement', confirm: 'Confirm settlement', pay: 'Record payment', adjust: 'Adjust commission',
  settlementHint: 'Includes all unsettled entries before the end of the selected month, including earlier months and losses. Only closed months can be settled.',
  confirmHint: 'Review the net commission, payable amount and carryforward before confirming. Payment can then be recorded.', payHint: 'Complete the offline payment first, then record the actual payment reference.',
  adjustmentHint: 'Positive amounts add commission; negative amounts reverse it. Link the original ledger ID where possible. Original usage remains unchanged.',
  sourceLedger: 'Original ledger ID (optional)', amount: 'Commission adjustment (USD)', saved: 'Saved', invalidDates: 'End date cannot precede start date.',
  errors: {
    SALES_DISABLED: 'The sales program is disabled.', SALES_NOT_FOUND: 'Partner or record not found.', SALES_CONFLICT: 'This operation conflicts with the current state. Refresh and review it.',
    SALES_INVALID: 'Check the inputs. Use an HTTPS origin, valid code and domain, and a valid commission rate.',
    SALES_PENDING_EVENTS: 'Usage events are still pending. Retry settlement after they are processed.', SALES_PAYOUT_FROZEN: 'Payouts are frozen for this partner.'
  }
}
