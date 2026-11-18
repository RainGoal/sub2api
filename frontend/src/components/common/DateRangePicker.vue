<template>
  <div class="relative min-w-0" ref="containerRef">
    <button
      type="button"
      :title="displayValue"
      @click="toggle"
      :class="['date-picker-trigger', isOpen && 'date-picker-trigger-open']"
    >
      <span class="date-picker-icon">
        <Icon name="calendar" size="sm" />
      </span>
      <span class="date-picker-value">
        {{ displayValue }}
      </span>
      <span class="date-picker-chevron">
        <Icon
          name="chevronDown"
          size="sm"
          :class="['transition-transform duration-200', isOpen && 'rotate-180']"
        />
      </span>
    </button>

    <Transition name="date-picker-dropdown">
      <div v-if="isOpen" class="date-picker-dropdown" :class="{ 'date-picker-with-time': enableTime, 'date-picker-custom-calendar': customCalendar && !enableTime }">
        <!-- Quick presets -->
        <div class="date-picker-presets">
          <button
            v-for="preset in presets"
            :key="preset.value"
            @click="selectPreset(preset)"
            :class="['date-picker-preset', isPresetActive(preset) && 'date-picker-preset-active']"
          >
            {{ t(preset.labelKey) }}
          </button>
        </div>

        <div class="date-picker-divider"></div>

        <!-- Custom date range inputs -->
        <div v-if="customCalendar && !enableTime" class="date-picker-calendar">
          <div class="date-picker-calendar-navigation">
            <button type="button" class="date-picker-calendar-nav-button" :aria-label="t('dates.previousMonth')" @click="shiftCalendarMonth(-1)">
              <Icon name="chevronLeft" size="sm" />
            </button>
            <span class="date-picker-calendar-range-label" aria-live="polite">
              {{ calendarMonthLabel(calendarMonths[0]!) }} <span aria-hidden="true">—</span> {{ calendarMonthLabel(calendarMonths[1]!) }}
            </span>
            <button type="button" class="date-picker-calendar-nav-button" :aria-label="t('dates.nextMonth')" :disabled="isNextCalendarMonthBeyondMaximum" @click="shiftCalendarMonth(1)">
              <Icon name="chevronRight" size="sm" />
            </button>
          </div>
          <div class="date-picker-calendar-selection">
            <span><span class="date-picker-calendar-selection-label">{{ t('dates.startDate') }}</span><strong>{{ localStartDate || '—' }}</strong></span>
            <Icon name="arrowRight" size="sm" class="text-gray-400" />
            <span><span class="date-picker-calendar-selection-label">{{ t('dates.endDate') }}</span><strong>{{ localEndDate || '—' }}</strong></span>
          </div>
          <p class="date-picker-calendar-hint">{{ t('dates.customRangeHint') }}</p>
          <div class="date-picker-calendar-months">
            <div v-for="month in calendarMonths" :key="`${month.getFullYear()}-${month.getMonth()}`" class="date-picker-calendar-month">
              <div class="date-picker-calendar-month-title">{{ calendarMonthLabel(month) }}</div>
              <div class="date-picker-calendar-weekdays">
                <span v-for="weekday in calendarWeekdays" :key="weekday">{{ weekday }}</span>
              </div>
              <div class="date-picker-calendar-days">
                <template v-for="(date, index) in calendarDateSlots(month)" :key="date ?? `empty-${index}`">
                  <span v-if="!date" class="date-picker-calendar-empty" aria-hidden="true"></span>
                  <button
                    v-else
                    type="button"
                    :data-date="date"
                    :aria-label="date"
                    :aria-pressed="date === localStartDate || date === localEndDate"
                    :disabled="date > maximumDate()"
                    :class="[
                      'date-picker-calendar-day',
                      (date === localStartDate || date === localEndDate) && 'date-picker-calendar-day-selected',
                      isCalendarDateInRange(date) && 'date-picker-calendar-day-in-range',
                    ]"
                    @click="selectCalendarDate(date)"
                  >{{ Number(date.slice(-2)) }}</button>
                </template>
              </div>
            </div>
          </div>
        </div>

        <div v-else class="date-picker-custom">
          <div class="date-picker-field">
            <label class="date-picker-label">{{ t(enableTime ? 'dates.startTime' : 'dates.startDate') }}</label>
            <input
              :type="enableTime ? 'datetime-local' : 'date'"
              :step="enableTime ? 1 : undefined"
              :aria-label="t(enableTime ? 'dates.startTime' : 'dates.startDate')"
              v-model="localStartDate"
              :max="localEndDate || maximumDate()"
              class="date-picker-input"
              @change="onDateChange"
            />
          </div>
          <div class="date-picker-separator">
            <Icon name="arrowRight" size="sm" class="text-gray-400" />
          </div>
          <div class="date-picker-field">
            <label class="date-picker-label">{{ t(enableTime ? 'dates.endTime' : 'dates.endDate') }}</label>
            <input
              :type="enableTime ? 'datetime-local' : 'date'"
              :step="enableTime ? 1 : undefined"
              :aria-label="t(enableTime ? 'dates.endTime' : 'dates.endDate')"
              v-model="localEndDate"
              :min="localStartDate"
              :max="maximumDate()"
              class="date-picker-input"
              @change="onDateChange"
            />
          </div>
        </div>

        <p v-if="enableTime" class="px-3 pb-2 text-xs text-gray-500">{{ t('dates.preciseRangeHint') }}</p>
        <!-- Apply button -->
        <div class="date-picker-actions">
          <button @click="apply" :disabled="!isValidRange" class="date-picker-apply disabled:cursor-not-allowed disabled:opacity-50">
            {{ t('dates.apply') }}
          </button>
        </div>
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { formatRangeInput, getLast24HoursRange } from '@/utils/usageTimeRange'

interface DatePreset {
  labelKey: string
  value: string
  getRange: () => { start: string; end: string }
}

interface Props {
  startDate: string
  endDate: string
  enableTime?: boolean
  customCalendar?: boolean
}

interface Emits {
  (e: 'update:startDate', value: string): void
  (e: 'update:endDate', value: string): void
  (e: 'change', range: { startDate: string; endDate: string; preset: string | null }): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t, locale } = useI18n()

const isOpen = ref(false)
const containerRef = ref<HTMLElement | null>(null)
const inputValue = (value: string, end = false) => props.enableTime ? formatRangeInput(value, end) : value
const localStartDate = ref(inputValue(props.startDate))
const localEndDate = ref(inputValue(props.endDate, true))
const activePreset = ref<string | null>('last24Hours')
const awaitingEndDate = ref(false)
const visibleCalendarMonth = ref(new Date(new Date().getFullYear(), new Date().getMonth(), 1))

const today = () => formatDateToString(new Date())

// Tomorrow's date - used for max date to handle timezone differences
// When user is in a timezone behind the server, "today" on server might be "tomorrow" locally
const tomorrow = () => {
  const d = new Date()
  d.setDate(d.getDate() + 1)
  return formatDateToString(d)
}
const maximumDate = () => props.enableTime ? `${tomorrow()}T23:59:59` : tomorrow()
const isValidRange = computed(() => {
  if (!localStartDate.value || !localEndDate.value) return false
  if (!props.enableTime) return localStartDate.value <= localEndDate.value
  const start = new Date(localStartDate.value).getTime()
  const end = new Date(localEndDate.value).getTime()
  return Number.isFinite(start) && Number.isFinite(end) && start < end
})

// Helper function to format date to YYYY-MM-DD using local timezone
const formatDateToString = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const calendarMonths = computed(() => [
  visibleCalendarMonth.value,
  new Date(visibleCalendarMonth.value.getFullYear(), visibleCalendarMonth.value.getMonth() + 1, 1),
])

const dateLocale = computed(() => locale.value === 'zh' ? 'zh-CN' : 'en-US')
const calendarWeekdays = computed(() => Array.from({ length: 7 }, (_, index) =>
  new Intl.DateTimeFormat(dateLocale.value, { weekday: 'short' }).format(new Date(2024, 0, 7 + index)),
))
const isNextCalendarMonthBeyondMaximum = computed(() => {
  const nextVisibleMonth = new Date(visibleCalendarMonth.value.getFullYear(), visibleCalendarMonth.value.getMonth() + 1, 1)
  return formatDateToString(nextVisibleMonth) > maximumDate()
})

const calendarMonthLabel = (month: Date) => new Intl.DateTimeFormat(dateLocale.value, { year: 'numeric', month: 'long' }).format(month)

const calendarDateSlots = (month: Date): Array<string | null> => {
  const firstWeekday = new Date(month.getFullYear(), month.getMonth(), 1).getDay()
  const daysInMonth = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate()
  return [
    ...Array.from({ length: firstWeekday }, () => null),
    ...Array.from({ length: daysInMonth }, (_, index) => formatDateToString(new Date(month.getFullYear(), month.getMonth(), index + 1))),
  ]
}

const shiftCalendarMonth = (offset: number) => {
  visibleCalendarMonth.value = new Date(visibleCalendarMonth.value.getFullYear(), visibleCalendarMonth.value.getMonth() + offset, 1)
}

const selectCalendarDate = (date: string) => {
  if (!awaitingEndDate.value || !localStartDate.value || localEndDate.value || date < localStartDate.value) {
    localStartDate.value = date
    localEndDate.value = ''
    awaitingEndDate.value = true
  } else {
    localEndDate.value = date
    awaitingEndDate.value = false
  }
  activePreset.value = null
}

const isCalendarDateInRange = (date: string) => Boolean(
  localStartDate.value && localEndDate.value && date > localStartDate.value && date < localEndDate.value,
)

const presets: DatePreset[] = [
  {
    labelKey: 'dates.today',
    value: 'today',
    getRange: () => {
      const t = today()
      return { start: t, end: t }
    }
  },
  {
    labelKey: 'dates.yesterday',
    value: 'yesterday',
    getRange: () => {
      const d = new Date()
      d.setDate(d.getDate() - 1)
      const yesterday = formatDateToString(d)
      return { start: yesterday, end: yesterday }
    }
  },
  {
    labelKey: 'dates.last24Hours',
    value: 'last24Hours',
    getRange: () => {
      const end = new Date()
      const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
      return {
        start: formatDateToString(start),
        end: formatDateToString(end)
      }
    }
  },
  {
    labelKey: 'dates.last7Days',
    value: '7days',
    getRange: () => {
      const end = today()
      const d = new Date()
      d.setDate(d.getDate() - 6)
      const start = formatDateToString(d)
      return { start, end }
    }
  },
  {
    labelKey: 'dates.last14Days',
    value: '14days',
    getRange: () => {
      const end = today()
      const d = new Date()
      d.setDate(d.getDate() - 13)
      const start = formatDateToString(d)
      return { start, end }
    }
  },
  {
    labelKey: 'dates.last30Days',
    value: '30days',
    getRange: () => {
      const end = today()
      const d = new Date()
      d.setDate(d.getDate() - 29)
      const start = formatDateToString(d)
      return { start, end }
    }
  },
  {
    labelKey: 'dates.thisMonth',
    value: 'thisMonth',
    getRange: () => {
      const now = new Date()
      const start = formatDateToString(new Date(now.getFullYear(), now.getMonth(), 1))
      return { start, end: today() }
    }
  },
  {
    labelKey: 'dates.lastMonth',
    value: 'lastMonth',
    getRange: () => {
      const now = new Date()
      const start = formatDateToString(new Date(now.getFullYear(), now.getMonth() - 1, 1))
      const end = formatDateToString(new Date(now.getFullYear(), now.getMonth(), 0))
      return { start, end }
    }
  }
]

const getPresetRange = (preset: DatePreset) => {
  const range = props.enableTime && preset.value === 'last24Hours'
    ? getLast24HoursRange()
    : preset.getRange()
  return { start: inputValue(range.start), end: inputValue(range.end, true) }
}

const displayValue = computed(() => {
  if (activePreset.value) {
    const preset = presets.find((p) => p.value === activePreset.value)
    if (preset) return t(preset.labelKey)
  }

  if (localStartDate.value && localEndDate.value) {
    if (localStartDate.value === localEndDate.value) {
      return formatDate(localStartDate.value)
    }
    return `${formatDate(localStartDate.value)} - ${formatDate(localEndDate.value)}`
  }

  return t('dates.selectDateRange')
})

const formatDate = (dateStr: string): string => {
  if (props.enableTime) return dateStr.replace('T', ' ')
  const date = new Date(dateStr + 'T00:00:00')
  const dateLocale = locale.value === 'zh' ? 'zh-CN' : 'en-US'
  return date.toLocaleDateString(dateLocale, { month: 'short', day: 'numeric' })
}

const isPresetActive = (preset: DatePreset): boolean => {
  return activePreset.value === preset.value
}

const selectPreset = (preset: DatePreset) => {
  const range = getPresetRange(preset)
  localStartDate.value = range.start
  localEndDate.value = range.end
  activePreset.value = preset.value
  awaitingEndDate.value = false
  setVisibleCalendarMonth(range.start)
}

const setVisibleCalendarMonth = (value: string) => {
  if (!value) return
  const date = new Date(`${value.slice(0, 10)}T00:00:00`)
  if (Number.isFinite(date.getTime())) {
    visibleCalendarMonth.value = new Date(date.getFullYear(), date.getMonth(), 1)
  }
}

const onDateChange = () => {
  // Check if current dates match any preset
  awaitingEndDate.value = false
  activePreset.value = null
  for (const preset of presets) {
    const range = getPresetRange(preset)
    if (range.start === localStartDate.value && range.end === localEndDate.value) {
      activePreset.value = preset.value
      break
    }
  }
}

const toggle = () => {
  isOpen.value = !isOpen.value
}

const apply = () => {
  if (!isValidRange.value) return
  const rollingRange = props.enableTime && activePreset.value === 'last24Hours' ? getLast24HoursRange() : null
  const startDate = rollingRange?.start ?? (props.enableTime ? new Date(localStartDate.value).toISOString() : localStartDate.value)
  const endDate = rollingRange?.end ?? (props.enableTime ? new Date(localEndDate.value).toISOString() : localEndDate.value)
  emit('update:startDate', startDate)
  emit('update:endDate', endDate)
  emit('change', {
    startDate,
    endDate,
    preset: activePreset.value
  })
  isOpen.value = false
}

const handleClickOutside = (event: MouseEvent) => {
  if (containerRef.value && !containerRef.value.contains(event.target as Node)) {
    isOpen.value = false
  }
}

const handleEscape = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && isOpen.value) {
    isOpen.value = false
  }
}

// Restore the applied range after dismissal, including parent updates from Apply.
watch(isOpen, (open) => {
  if (open) {
    setVisibleCalendarMonth(props.startDate)
    awaitingEndDate.value = false
    return
  }
  localStartDate.value = inputValue(props.startDate)
  localEndDate.value = inputValue(props.endDate, true)
  onDateChange()
}, { flush: 'post' })

// Sync local state with props
watch(
  () => props.startDate,
  (val) => {
    localStartDate.value = inputValue(val)
    onDateChange()
    setVisibleCalendarMonth(val)
  }
)

watch(
  () => props.endDate,
  (val) => {
    localEndDate.value = inputValue(val, true)
    onDateChange()
  }
)

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleEscape)
  // Initialize active preset detection
  onDateChange()
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleEscape)
})
</script>

<style scoped>
.date-picker-trigger {
  @apply flex max-w-full items-center gap-2;
  @apply rounded-lg px-3 py-2 text-sm;
  @apply bg-white dark:bg-dark-800;
  @apply border border-gray-200 dark:border-dark-600;
  @apply text-gray-700 dark:text-gray-300;
  @apply transition-all duration-200;
  @apply focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500/30;
  @apply hover:border-gray-300 dark:hover:border-dark-500;
  @apply cursor-pointer;
}

.date-picker-trigger-open {
  @apply border-primary-500 ring-2 ring-primary-500/30;
}

.date-picker-icon {
  @apply text-gray-400 dark:text-dark-400;
}

.date-picker-value {
  @apply truncate font-medium;
}

.date-picker-chevron {
  @apply text-gray-400 dark:text-dark-400;
}

.date-picker-dropdown {
  @apply absolute left-0 z-[100] mt-2;
  @apply bg-white dark:bg-dark-800;
  @apply rounded-xl;
  @apply border border-gray-200 dark:border-dark-700;
  @apply shadow-lg shadow-black/10 dark:shadow-black/30;
  @apply overflow-hidden;
  @apply min-w-[320px];
}

.date-picker-presets {
  @apply grid grid-cols-2 gap-1 p-2;
}

.date-picker-preset {
  @apply rounded-md px-3 py-1.5 text-xs font-medium;
  @apply text-gray-600 dark:text-gray-400;
  @apply hover:bg-gray-100 dark:hover:bg-dark-700;
  @apply transition-colors duration-150;
}

.date-picker-preset-active {
  @apply bg-primary-100 dark:bg-primary-900/30;
  @apply text-primary-700 dark:text-primary-300;
}

.date-picker-divider {
  @apply border-t border-gray-100 dark:border-dark-700;
}

.date-picker-custom {
  @apply flex items-end gap-2 p-3;
}

.date-picker-custom-calendar {
  width: min(660px, calc(100vw - 2rem));
  min-width: 0;
}

.date-picker-calendar {
  @apply px-3 pb-3;
}

.date-picker-calendar-navigation {
  @apply flex items-center justify-between gap-2 px-1 pb-2;
}

.date-picker-calendar-nav-button {
  @apply flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg;
  @apply text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900;
  @apply dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white;
  @apply focus:outline-none focus:ring-2 focus:ring-primary-500/40;
}

.date-picker-calendar-nav-button:disabled {
  @apply cursor-not-allowed opacity-40;
}

.date-picker-calendar-range-label {
  @apply min-w-0 truncate text-center text-xs font-medium text-gray-500 dark:text-gray-400;
}

.date-picker-calendar-selection {
  @apply mb-2 flex items-center justify-center gap-3 rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-900/60;
}

.date-picker-calendar-selection > span {
  @apply flex min-w-0 flex-col items-center gap-0.5;
}

.date-picker-calendar-selection-label {
  @apply text-[10px] font-medium text-gray-400 dark:text-gray-500;
}

.date-picker-calendar-selection strong {
  @apply truncate text-xs font-semibold text-gray-800 dark:text-gray-100;
}

.date-picker-calendar-hint {
  @apply pb-2 text-center text-[11px] text-gray-500 dark:text-gray-400;
}

.date-picker-calendar-months {
  @apply grid grid-cols-2 gap-4;
}

.date-picker-calendar-month-title {
  @apply pb-2 text-center text-sm font-semibold text-gray-900 dark:text-white;
}

.date-picker-calendar-weekdays,
.date-picker-calendar-days {
  @apply grid grid-cols-7 gap-1 text-center;
}

.date-picker-calendar-weekdays {
  @apply mb-1 text-[11px] font-medium text-gray-400 dark:text-gray-500;
}

.date-picker-calendar-day,
.date-picker-calendar-empty {
  @apply mx-auto flex h-8 w-8 items-center justify-center rounded-full text-xs;
}

.date-picker-calendar-day {
  @apply text-gray-700 transition-colors hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700;
  @apply focus:outline-none focus:ring-2 focus:ring-primary-500/40;
}

.date-picker-calendar-day:disabled {
  @apply cursor-not-allowed text-gray-300 hover:bg-transparent dark:text-dark-500 dark:hover:bg-transparent;
}

.date-picker-calendar-day-in-range {
  @apply rounded-md bg-primary-50 text-primary-800 dark:bg-primary-900/30 dark:text-primary-200;
}

.date-picker-calendar-day-selected,
.date-picker-calendar-day-selected:hover {
  @apply bg-primary-600 font-semibold text-white dark:bg-primary-500 dark:text-white;
}

.date-picker-calendar-day-selected:disabled {
  @apply bg-gray-300 text-white dark:bg-dark-600 dark:text-gray-400;
}

.date-picker-field {
  @apply min-w-0 flex-1;
}

.date-picker-with-time {
  width: min(560px, calc(100vw - 2rem));
  min-width: 0;
}

@media (max-width: 600px) {
  .date-picker-calendar-months > :nth-child(2) {
    display: none;
  }
  .date-picker-calendar-day,
  .date-picker-calendar-empty {
    width: 2rem;
    height: 2rem;
  }
  .date-picker-with-time {
    position: fixed;
    inset: 20vh 1rem auto;
    width: auto;
    max-height: 70vh;
    overflow-y: auto;
  }
  .date-picker-with-time .date-picker-custom {
    @apply flex-col items-stretch;
  }
  .date-picker-with-time .date-picker-separator {
    @apply hidden;
  }
}

.date-picker-label {
  @apply mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400;
}

.date-picker-input {
  @apply w-full rounded-md px-2 py-1.5 text-sm;
  @apply bg-gray-50 dark:bg-dark-700;
  @apply border border-gray-200 dark:border-dark-600;
  @apply text-gray-900 dark:text-gray-100;
  @apply focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500/30;
}

.date-picker-input::-webkit-calendar-picker-indicator {
  @apply cursor-pointer opacity-60 hover:opacity-100;
  filter: invert(0.5);
}

.dark .date-picker-input::-webkit-calendar-picker-indicator {
  filter: none;
}

.date-picker-separator {
  @apply flex items-center justify-center pb-1;
}

.date-picker-actions {
  @apply flex justify-end p-2 pt-0;
}

.date-picker-apply {
  @apply rounded-lg px-4 py-1.5 text-sm font-medium;
  @apply bg-primary-600 text-white;
  @apply hover:bg-primary-700;
  @apply transition-colors duration-150;
}

/* Dropdown animation */
.date-picker-dropdown-enter-active,
.date-picker-dropdown-leave-active {
  transition: all 0.2s ease;
}

.date-picker-dropdown-enter-from,
.date-picker-dropdown-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
