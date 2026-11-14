import { adminThemeRevision } from './theme'

const chartTokens = {
  text: ['--ws-text-soft', '#d1d0c9'],
  muted: ['--ws-muted', '#a8a79f'],
  grid: ['--ws-line', '#3a3935'],
  panel: ['--ws-panel', '#252523'],
  border: ['--ws-line-strong', '#53514a'],
  title: ['--ws-text', '#f0efeb'],
  accent: ['--ws-chart-1', '#ee6a43'],
  secondary: ['--ws-chart-2', '#f0efeb'],
  neutral: ['--ws-chart-3', '#918d85'],
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
    adminChartColor('accent', original[0] || '#ee6a43'),
    adminChartColor('secondary', original[1] || '#f0efeb'),
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
    backgroundColor: adminChartColor('panel', '#252523'),
    titleColor: adminChartColor('title', '#f0efeb'),
    bodyColor: adminChartColor('text', '#d1d0c9'),
    footerColor: adminChartColor('muted', '#a8a79f'),
    borderColor: adminChartColor('border', '#53514a'),
    borderWidth: 1,
    cornerRadius: 6
  }
}
