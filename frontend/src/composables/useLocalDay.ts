import { onMounted, onUnmounted, readonly, ref } from 'vue'

// useLocalDay.ts maintains the browser's current calendar date for mounted views
// and date controls. A timeout targets the next local midnight, using calendar
// components so daylight-saving changes do not turn the schedule into a fixed
// twenty-four-hour interval. Consumers decide whether a changed day should also
// change their selected range or start a request.
//
// Browsers can suspend timers while a tab is hidden or the device sleeps. Focus
// and visibility events resample the wall clock and replace the pending timeout.
// Interactive callers can request the same resampling before using a date. Each
// component owns its timer and listeners, which are released when it unmounts.

// formatLocalDay formats calendar components without converting the date to UTC.
function formatLocalDay(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

// useLocalDay supplies a readonly YYYY-MM-DD date and an immediate refresh action.
// Call it during component setup, and call refreshToday only while mounted. The
// date follows browser-local midnight and catches up when a suspended tab resumes.
//
// Every refresh replaces the previous timeout using the current wall clock, so
// manual interaction and resume events leave only one scheduled rollover. No
// selected range is changed here; consumers retain control of fixed historical
// dates and any requests triggered by a new day.
export function useLocalDay() {
  const today = ref(formatLocalDay(new Date()))
  let midnightTimer: ReturnType<typeof setTimeout> | undefined

  // refreshToday resamples the local date and schedules its next midnight change.
  // It returns the current date immediately so event handlers can use fresh bounds
  // even when a browser has delayed the previous timer.
  function refreshToday(): string {
    const now = new Date()
    today.value = formatLocalDay(now)
    // Replace the pending timeout after a resume or manual refresh so both paths
    // cannot leave separate rollover callbacks scheduled for the same component.
    clearTimeout(midnightTimer)
    const nextMidnight = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1)
    midnightTimer = setTimeout(refreshToday, nextMidnight.getTime() - now.getTime())
    return today.value
  }

  // onVisibilityChange catches up when the browser exposes this document again.
  // Hidden transitions leave the existing timer alone; becoming visible resamples
  // the wall clock before consumers choose whether to reload their data.
  function onVisibilityChange() {
    if (document.visibilityState === 'visible') {
      refreshToday()
    }
  }

  // Install resume listeners with the first timer so a mounted component observes
  // both normal midnight callbacks and wall-clock changes after browser suspension.
  onMounted(() => {
    refreshToday()
    window.addEventListener('focus', refreshToday)
    document.addEventListener('visibilitychange', onVisibilityChange)
  })

  // Release the timer and both listeners together so removed components cannot
  // publish later date changes or keep rescheduling work after navigation.
  onUnmounted(() => {
    clearTimeout(midnightTimer)
    window.removeEventListener('focus', refreshToday)
    document.removeEventListener('visibilitychange', onVisibilityChange)
  })

  return { today: readonly(today), refreshToday }
}
