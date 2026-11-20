import { readonly, ref, watch, type Ref } from 'vue'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'

// useAnchoredPanel.ts computes horizontal style offsets for dropdown panels positioned
// relative to an in-flow trigger wrapper. It converts rem sizing with the root font size,
// delegates viewport clamping to getFloatingPanelPosition, and keeps the panel inside
// viewport margins while open and across viewport resizes.

// AnchoredPanelOptions configures preferred width, trigger alignment, and viewport
// clearance for a relatively positioned dropdown panel.
export interface AnchoredPanelOptions {
  widthRem: number // preferred width in rem

  // Selects left or right trigger edge alignment before viewport clamping; defaults
  // to right.
  align?: 'left' | 'right'

  // Reserves horizontal viewport clearance in root-font rem units; defaults to one
  // rem.
  viewportPaddingRem?: number
}

// useAnchoredPanel tracks an open relatively positioned dropdown and returns inline
// width and left offsets clamped to the viewport. Supply reactive anchor and open refs
// during setup; measuring runs synchronously when the panel opens so the first rendered
// frame uses clamped coordinates.
//
// While open with a mounted anchor, the composable listens for window resize events
// and removes that listener when the panel closes or the owning component unmounts.
// Missing DOM nodes or non-browser environments leave the current style unchanged.
export function useAnchoredPanel(
  anchor: Readonly<Ref<HTMLElement | null>>,
  open: Readonly<Ref<boolean>>,
  options: AnchoredPanelOptions,
) {
  // panelStyle stores the wrapper-relative horizontal offset and clamped width applied
  // directly to the dropdown element.
  const panelStyle = ref<Record<string, string>>({})

  // updatePosition measures the anchor wrapper, delegates horizontal clamping to the
  // shared floating-panel helper, and writes wrapper-relative left and width values.
  function updatePosition() {
    if (typeof window === 'undefined' || typeof document === 'undefined') return
    const rect = anchor.value?.getBoundingClientRect()
    if (!rect) return
    // Convert rem sizing using the root font size; fall back to sixteen pixels when
    // computed styles report an unavailable or non-numeric font size.
    const rem = Number.parseFloat(getComputedStyle(document.documentElement).fontSize) || 16
    const viewportWidth = document.documentElement.clientWidth || window.innerWidth
    const viewportPadding = (options.viewportPaddingRem ?? 1) * rem
    const position = getFloatingPanelPosition(rect, viewportWidth, window.innerHeight, {
      maxWidth: options.widthRem * rem,
      align: options.align ?? 'right',
      viewportPadding,
    })
    panelStyle.value = {
      width: `${position.width}px`,
      left: `${position.left - rect.left}px`,
    }
  }

  // Synchronize placement on opening before Vue paints the panel, and bind viewport
  // resize tracking strictly to the open lifetime so closed panels carry no listeners.
  watch(
    [anchor, open],
    ([element, isOpen], _previous, onCleanup) => {
      if (!isOpen || !element || typeof window === 'undefined') return
      updatePosition()
      window.addEventListener('resize', updatePosition)
      onCleanup(() => {
        window.removeEventListener('resize', updatePosition)
      })
    },
    { immediate: true, flush: 'sync' },
  )

  return {
    panelStyle: readonly(panelStyle),
    updatePosition,
  }
}
