import { describe, expect, it } from 'vitest'
import { COMPOSITE_TARGET_PLATFORM_OPTIONS } from '@/constants/platforms'

describe('GroupsView Composite route options', () => {
  it('offers Kimi, Zhipu GLM, and DeepSeek as route targets', () => {
    expect(COMPOSITE_TARGET_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(
      expect.arrayContaining(['kimi', 'zhipu', 'deepseek'])
    )
    expect(COMPOSITE_TARGET_PLATFORM_OPTIONS.map((option) => option.value)).not.toContain('seedance')
  })
})
