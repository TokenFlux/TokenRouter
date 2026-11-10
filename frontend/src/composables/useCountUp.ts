import { onBeforeUnmount, ref, toValue, watch, type MaybeRefOrGetter } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'

// useCountUp.ts animates numeric changes from the currently displayed value to each new
// target. A replacement target cancels the pending frame and starts from the intermediate
// display value, allowing successive updates to remain visually continuous. A reset key
// separates values that should not interpolate across contexts, such as different currencies.
//
// A single frame handle belongs to the current animation, and component teardown cancels
// that handle. Progress uses elapsed time divided by the supplied duration, with a quartic
// ease-out curve and an exact final assignment. Callers provide a positive duration in
// milliseconds. Changes to the reset key or reduced-motion preference can stop interpolation
// and publish the current target before the next frame.

// useCountUp returns a reactive value that eases toward changes in source. Invoke it
// during component setup and supply a positive millisecond duration. Every target change
// cancels the prior frame before reading the current displayed value, so a replacement
// animation starts from the visible intermediate number.
//
// Pass a reset key for changes in unit or context that should snap to the target. Reduced
// motion, nonfinite values, and missing frame support also bypass interpolation. The
// unmount hook cancels unfinished work, and completion writes the exact target.
export function useCountUp(
  source: MaybeRefOrGetter<number>,
  duration: number,
  resetKey?: MaybeRefOrGetter<string>,
) {
  const reducedMotion = usePreferredReducedMotion()
  // display holds the visible interpolation value, while frame identifies the pending
  // callback that replacement targets and teardown cancel.
  const display = ref(toValue(source))
  let frame = 0

  // cancel stops the pending animation frame and clears its handle. It is safe to call
  // repeatedly before replacement animations and during component teardown when no frame
  // remains.
  const cancel = () => {
    // Cancel the queued callback before forgetting its handle so replacement targets and
    // teardown leave no earlier animation scheduled.
    if (frame) cancelAnimationFrame(frame)
    frame = 0
  }

  watch(
    [() => toValue(source), () => toValue(resetKey), reducedMotion],
    ([target, key, preference], [, previousKey]) => {
      // Cancel the prior callback before capturing display so a previous target cannot
      // advance the new animation's starting value.
      cancel()
      const from = display.value
      // Publish the new target directly when the unit changes, the preference disables motion,
      // or interpolation would encounter a nonfinite value.
      if (
        from === target ||
        key !== previousKey ||
        preference === 'reduce' ||
        !Number.isFinite(from) ||
        !Number.isFinite(target) ||
        typeof requestAnimationFrame !== 'function'
      ) {
        display.value = target
        return
      }
      const startedAt = performance.now()
      // tick advances the active count animation using its captured starting value and
      // target. Frame timestamps determine progress relative to the start time, and
      // progress is capped at one before applying quartic easing.
      //
      // An unfinished tick schedules its successor and replaces the shared frame handle.
      // Completion assigns the exact target and clears the handle, allowing cancellation
      // to distinguish finished work from a frame still waiting to run.
      const tick = (now: number) => {
        // Cap elapsed progress at completion before interpolation overshoots.
        const progress = Math.min((now - startedAt) / duration, 1)
        const eased = 1 - Math.pow(1 - progress, 4)
        display.value = from + (target - from) * eased
        if (progress < 1) {
          frame = requestAnimationFrame(tick)
          return
        }
        display.value = target
        frame = 0
      }
      frame = requestAnimationFrame(tick)
    },
  )

  onBeforeUnmount(cancel)

  return display
}
