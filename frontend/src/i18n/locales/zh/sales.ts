export default {
  title: '销售分站', description: '管理推广入口、客户归属和消费毛利分成。', settings: '分站设置', enabled: '开启销售分站',
  mainUrl: '主站地址', mainUrlHint: '填写 HTTPS 主站根地址；注册、登录和支付继续使用主站。',
  addPartner: '新增销售', editPartner: '编辑销售', partners: '销售伙伴', customers: '客户', ledger: '分成账本', settlements: '结算单',
  name: '销售名称', userId: '平台用户 ID', userIdHint: '销售使用已有平台账户登录；创建后不能更换绑定账户。',
  code: '推广代码', hostname: '专属域名', hostnameHint: '只填域名，不含协议或路径。DNS 与 HTTPS 跳转需单独配置。',
  codeHint: '3–48 位英文字母、数字或短横线，以字母或数字开头，例如 sales-qq。大写字母会自动转为小写。',
  commission_rate: '分成比例', promotion_enabled: '允许新增客户', accrual_enabled: '继续计提分成', payout_frozen: '冻结付款',
  ruleHint: '比例与计提状态修改立即影响后续消费，历史账目保持原值。', active: '开启', inactive: '关闭', frozen: '已冻结',
  view: '查看客户与账目', back: '返回销售列表', search: '搜索名称、代码或域名', referral: '主站推广链接', domain: '专属入口',
  referralHint: '将专属域名重定向到此链接；客户注册完成后绑定，支付回调保持主域名。',
  revenue: '消费收入', cost: '约定成本', profit: '结算毛利', commission: '分成', customer_count: '累计客户',
  unsettled_commission: '未入账单分成', pending_payout: '待付款', paid_commission: '已付款', pending: '{count} 笔消费待入账',
  scope: '余额按量消费；按扣减余额计收入。金额均为 USD，赠送和优惠由平台承担。',
  dateHint: '日期按 UTC 统计；客户数和付款余额为累计值。', start: '开始日期', end: '结束日期', apply: '应用筛选',
  empty: '暂无记录', emptyHint: '完成推广注册和消费后，相关数据会显示在这里。', retry: '重新加载', loadError: '销售数据加载失败，请重试。',
  created_at: '注册时间', occurred_at: '发生时间', email: '客户邮箱', customer_user_id: '客户 ID', kind: '类型',
  usage: '消费', adjustment: '调整', carry: '亏损结转', note: '说明', settlement_id: '结算单号', month: '结算月份（UTC）',
  net_commission: '账单净分成', payout_amount: '应付金额', carry_amount: '结转金额', status: '状态', draft: '待审核', confirmed: '待付款', paid: '已付款',
  payment_reference: '付款凭证 / 流水号', createSettlement: '生成结算单', confirm: '确认结算单', pay: '登记付款', adjust: '新增分成调整',
  settlementHint: '汇总截至所选月份月底的全部未结算账目；包含以前月份和负分成。仅可结算已结束的月份。',
  confirmHint: '请核对账单净分成、应付金额和结转金额，确认后可登记付款。', payHint: '请先完成线下付款，再登记真实的付款凭证。',
  adjustmentHint: '正数补计分成，负数冲减分成。建议填写原账本 ID 以便追溯；不会修改原始消费记录。',
  sourceLedger: '原账本 ID（可选）', amount: '调整分成金额（USD）', saved: '已保存', invalidDates: '结束日期不能早于开始日期。',
  validation: {
    main_frontend_url: '主站地址需为 HTTPS 根地址，例如 https://ai.yusflow.com，不含路径、查询参数或片段。',
    user_id: '平台用户 ID 无效：请填写已注册普通用户的 ID；管理员账号不可作为销售，创建后不能更换绑定账号。',
    name: '请填写销售名称；名称过长时请缩短后重试。',
    code: '推广代码需为 3–48 位英文字母、数字或短横线，并以字母或数字开头，例如 sales-qq。',
    hostname: '专属域名需填写完整子域名，例如 qq.yusflow.com，不含 https://、端口或路径。',
    commission_rate: '分成比例需为 0–100 的数字；例如 50% 请填 50。'
  },
  errors: {
    SALES_DISABLED: '销售分站尚未开启。', SALES_NOT_FOUND: '销售或账目不存在。', SALES_CONFLICT: '操作与当前状态冲突，请刷新后核对。',
    SALES_INVALID: '输入内容无效，请按对应字段的填写说明检查后重试。',
    SALES_PENDING_EVENTS: '仍有消费待入账，暂不能结算，请稍后重试。', SALES_PAYOUT_FROZEN: '该销售付款已被冻结。'
  }
}
