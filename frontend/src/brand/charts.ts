import { adminThemeRevision } from './theme'

const chartTokens = {
  text: ['--ws-text-soft', '#b0b0b0'],
  muted: ['--ws-muted', '#a3a3a3'],
  grid: ['--ws-line', '#2b2b2b'],
  panel: ['--ws-panel', '#1b1b1b'],
  border: ['--ws-line-strong', '#3b3b3b'],
  title: ['--ws-text', '#ededed'],
  accent: ['--ws-chart-1', '#f06a42'],
  secondary: ['--ws-chart-2', '#f0f0f0'],
  neutral: ['--ws-chart-3', '#8a8a8a'],
  positive: ['--ws-success', '#4ade80'],
  warning: ['--ws-warning', '#f0b84d'],
  negative: ['--ws-danger', '#ff7b72'],
  info: ['--ws-info', '#66b3ff']
} as const

type ChartToken = keyof typeof chartTokens

function hasAdminBrand(): boolean {
  // Track scope/theme changes when called from a chart's computed data or options.
  void adminThemeRevision.value
  return typeof document !== 'undefined' && document.documentElement.dataset.adminBrand === 'wsapi'
}

/** Canvas cannot resolve CSS variables; retain the original color outside the admin workspace. */
export function adminChartColor(token: ChartToken, fallback: string): string {
  if (!hasAdminBrand()) return fallback
  const [property, defaultValue] = chartTokens[token]
  return getComputedStyle(document.documentElement).getPropertyValue(property).trim() || defaultValue
}

/** Keep category colors distinct; semantic status datasets use adminChartColor instead. */
export function adminChartPalette(original: readonly string[]): string[] {
  if (!hasAdminBrand()) return [...original]
  const colors = [
    adminChartColor('accent', original[0] || chartTokens.accent[1]),
    adminChartColor('secondary', original[1] || chartTokens.secondary[1]),
    ...(document.documentElement.classList.contains('dark')
      ? ['#86b7c7', '#c7a7d7', '#a9be82', '#d8b77d', '#cf8fa4', '#8cb4a2', '#a3a7d6', '#cba38c', '#a3bac7', '#b9b4a7']
      : ['#28758c', '#81549d', '#55752c', '#916510', '#a24b6a', '#39765a', '#5e64a3', '#996643', '#52768a', '#797064'])
  ]
  return original.map((_, index) => colors[index % colors.length])
}

export function adminChartFill(color: string, fallback: string): string {
  if (!hasAdminBrand()) return fallback
  return /^#[\da-f]{6}$/i.test(color) ? `${color}1a` : fallback
}

/** Only add overrides for the admin scope, preserving the caller's tooltip defaults elsewhere. */
export function adminChartTooltip() {
  if (!hasAdminBrand()) return {}
  return {
    backgroundColor: adminChartColor('panel', chartTokens.panel[1]),
    titleColor: adminChartColor('title', chartTokens.title[1]),
    bodyColor: adminChartColor('text', chartTokens.text[1]),
    footerColor: adminChartColor('muted', chartTokens.muted[1]),
    borderColor: adminChartColor('border', chartTokens.border[1]),
    borderWidth: 1,
    cornerRadius: 6
  }
}
