import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import BackupView from '../BackupView.vue'

const {
  getS3Config,
  getImageStorageConfig,
  updateImageStorageConfig,
  testImageStorageConnection,
  showError,
  getSchedule,
  listBackups,
  getDownloadURL,
} = vi.hoisted(() => ({
  getS3Config: vi.fn(),
  getImageStorageConfig: vi.fn(),
  updateImageStorageConfig: vi.fn(),
  testImageStorageConnection: vi.fn(),
  showError: vi.fn(),
  getSchedule: vi.fn(),
  listBackups: vi.fn(),
  getDownloadURL: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    backup: {
      getS3Config,
      updateS3Config: vi.fn(),
      testS3Connection: vi.fn(),
      getImageStorageConfig,
      updateImageStorageConfig,
      testImageStorageConnection,
      getSchedule,
      updateSchedule: vi.fn(),
      createBackup: vi.fn(),
      listBackups,
      getBackup: vi.fn(),
      deleteBackup: vi.fn(),
      getDownloadURL,
      restoreBackup: vi.fn(),
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: (fn: () => unknown) => fn() }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => '',
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params?.index === undefined ? key : `${key}:${params.index}`,
  }),
}))

const baseRecord = (id: string, parts?: unknown[]) => ({
  id,
  status: 'completed',
  backup_type: 'postgres',
  file_name: `${id}.sql.gz`,
  s3_key: `backups/${id}.sql.gz`,
  parts,
  size_bytes: 10,
  triggered_by: 'manual',
  started_at: '2026-08-09T00:00:00Z',
})

function mountBackupView() {
  return mount(BackupView, {
    global: {
      stubs: {
        TotpStepUpDialog: true,
      },
    },
  })
}

describe('admin BackupView 视频素材上传限额', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    vi.clearAllMocks()
    getS3Config.mockResolvedValue({})
    getImageStorageConfig.mockResolvedValue({ config: {}, secret_configured: false })
    getSchedule.mockResolvedValue({ enabled: false, cron_expr: '', retain_days: 14, retain_count: 10 })
    listBackups.mockResolvedValue({ items: [] })
    updateImageStorageConfig.mockResolvedValue({})
    testImageStorageConnection.mockResolvedValue({ ok: true })
  })

  afterEach(() => { wrapper?.unmount() })

  async function clickImageStorageAction(key: string) {
    const card = wrapper.findAll('.card').find((item) => item.text().includes('admin.backup.imageStorage.title'))!
    await card.findAll('button').find((button) => button.text() === key)!.trigger('click')
    await flushPromises()
  }

  it('兼容旧响应，显示并发送默认限额', async () => {
    wrapper = mountBackupView()
    await flushPromises()
    expect((wrapper.get('#video-upload-max-per-minute').element as HTMLInputElement).value).toBe('20')
    expect((wrapper.get('#video-upload-daily-limit-mib').element as HTMLInputElement).value).toBe('1024')
    await clickImageStorageAction('admin.backup.s3.testConnection')
    await clickImageStorageAction('common.save')
    const defaults = { video_upload_max_per_minute: 20, video_upload_daily_limit_mib: 1024 }
    expect(testImageStorageConnection).toHaveBeenCalledWith(expect.objectContaining(defaults))
    expect(updateImageStorageConfig).toHaveBeenCalledWith(expect.objectContaining(defaults))
  })

  it.each([
    [1, 1],
    [10000, 1048576],
    [75, 2048],
  ])('支持保存和测试合法限额 %i 次/分钟、%i MiB/日', async (perMinute, dailyMiB) => {
    wrapper = mountBackupView()
    await flushPromises()
    await wrapper.get('#video-upload-max-per-minute').setValue(String(perMinute))
    await wrapper.get('#video-upload-daily-limit-mib').setValue(String(dailyMiB))
    await clickImageStorageAction('admin.backup.s3.testConnection')
    await clickImageStorageAction('common.save')
    const limits = { video_upload_max_per_minute: perMinute, video_upload_daily_limit_mib: dailyMiB }
    expect(testImageStorageConnection).toHaveBeenCalledWith(expect.objectContaining(limits))
    expect(updateImageStorageConfig).toHaveBeenCalledWith(expect.objectContaining(limits))
  })

  it.each([
    ['video-upload-max-per-minute', '', 'videoUploadMaxPerMinuteInvalid'],
    ['video-upload-max-per-minute', '0', 'videoUploadMaxPerMinuteInvalid'],
    ['video-upload-max-per-minute', '-1', 'videoUploadMaxPerMinuteInvalid'],
    ['video-upload-max-per-minute', '1.5', 'videoUploadMaxPerMinuteInvalid'],
    ['video-upload-max-per-minute', '10001', 'videoUploadMaxPerMinuteInvalid'],
    ['video-upload-daily-limit-mib', '', 'videoUploadDailyLimitMiBInvalid'],
    ['video-upload-daily-limit-mib', '0', 'videoUploadDailyLimitMiBInvalid'],
    ['video-upload-daily-limit-mib', '-1', 'videoUploadDailyLimitMiBInvalid'],
    ['video-upload-daily-limit-mib', '1.5', 'videoUploadDailyLimitMiBInvalid'],
    ['video-upload-daily-limit-mib', '1048577', 'videoUploadDailyLimitMiBInvalid'],
  ])('阻止非法字段 %s=%s 发往保存或连接测试接口', async (field, value, errorKey) => {
    wrapper = mountBackupView()
    await flushPromises()
    await wrapper.get(`#${field}`).setValue(value)
    await clickImageStorageAction('common.save')
    await clickImageStorageAction('admin.backup.s3.testConnection')
    expect(updateImageStorageConfig).not.toHaveBeenCalled()
    expect(testImageStorageConnection).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith(`admin.backup.imageStorage.${errorKey}`)
  })
})

describe('admin BackupView 分卷备份', () => {
  beforeEach(() => {
    getS3Config.mockResolvedValue({})
    getImageStorageConfig.mockResolvedValue({ config: {}, secret_configured: false })
    getSchedule.mockResolvedValue({ enabled: false, cron_expr: '', retain_days: 14, retain_count: 10 })
    getDownloadURL.mockReset()
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('显示分卷数并在下载时列出每个分卷链接', async () => {
    listBackups.mockResolvedValue({
      items: [baseRecord('split', [{ index: 1 }, { index: 2 }, { index: 3 }])],
    })
    getDownloadURL.mockResolvedValue({
      parts: [
        { index: 1, size_bytes: 5, url: 'https://example.test/part-1' },
        { index: 2, size_bytes: 6, url: 'https://example.test/part-2' },
        { index: 3, size_bytes: 7, url: 'https://example.test/part-3' },
      ],
    })

    const wrapper = mountBackupView()
    await flushPromises()

    expect(wrapper.text()).toContain('3')
    const downloadButton = wrapper.findAll('button').find(button =>
      button.text().includes('admin.backup.actions.download'),
    )
    expect(downloadButton).toBeDefined()
    await downloadButton!.trigger('click')
    await flushPromises()

    expect(document.body.textContent).toContain('admin.backup.actions.partLabel:1')
    expect(document.body.textContent).toContain('admin.backup.actions.partLabel:3')
    expect(document.body.querySelector('a[href="https://example.test/part-2"]')).not.toBeNull()
  })

  it('旧单文件记录仍使用单个下载地址', async () => {
    listBackups.mockResolvedValue({ items: [baseRecord('legacy')] })
    getDownloadURL.mockResolvedValue({ url: 'https://example.test/legacy.sql.gz' })

    const wrapper = mountBackupView()
    await flushPromises()
    const downloadButton = wrapper.findAll('button').find(button =>
      button.text().includes('admin.backup.actions.download'),
    )
    await downloadButton!.trigger('click')
    await flushPromises()

    expect(getDownloadURL).toHaveBeenCalledWith('legacy')
    expect(document.body.textContent).not.toContain('admin.backup.actions.downloadParts')
  })

  it('运行中的备份不显示删除入口', async () => {
    listBackups.mockResolvedValue({
      items: [{ ...baseRecord('running'), status: 'running', progress: 'uploading' }],
    })

    const wrapper = mountBackupView()
    await flushPromises()

    expect(wrapper.find('tbody tr td:nth-child(5)').text()).toBe('-')
    expect(wrapper.findAll('button').some(button => button.text() === 'common.delete')).toBe(false)
  })
})
