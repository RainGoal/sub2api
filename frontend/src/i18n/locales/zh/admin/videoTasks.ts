export default {
  videoTasks: {
    connection: '视频任务连接',
    bblabuPreset: '应用 BBLabu 连接预设',
    supplierName: '供应商名称',
    explicitBaseUrl: '填写下方 Base URL 和 API Key。自定义供应商使用相同协议；连接预设不会自动启用模型。',
    baseUrlRequired: 'Video Tasks JSON v1 必须填写 Base URL。',
    discover: '同步发现上游模型',
    discovering: '正在发现…',
    discoveryHint: '发现结果仅供核对；需要手动勾选模型并配置采购价和售价。',
    saveBeforeDiscovery: '连接地址已修改：请填写新 Key 预览，或保存后使用现有 Key 同步。',
    discoveredCount: '发现 {count} 个模型，未自动授权',
    noDiscoveredModels: '上游未返回模型。',
    discoveryFailed: '模型发现失败，请检查连接、Key 和权限。',
    enabledModels: '授权的视频模型',
    search: '搜索精确型号',
    loadingTemplates: '正在加载服务端能力模板…',
    templatesFailed: '能力模板加载失败，请重试或检查管理员权限。',
    retry: '重新加载',
    noMatchingModels: '没有匹配的能力模板，可在下方配置自定义型号。',
    chooseModels: '请明确选择至少一个有能力配置的视频模型。',
    capabilityJSON: '自定义模型能力 JSON',
    capabilityHint: '编辑能力对象数组，或先勾选模板再修改。完整字段由后端校验；未确认的参数组合保持禁用。新增型号还需单独勾选授权。',
    invalidJSON: '能力配置必须是有效的 JSON 对象数组，每项必须包含 model。',
    fixedSpecification: '固定规格',
    perVideo: '按条计费',
    perSecond: '按秒计费'
  }
}
