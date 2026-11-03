// overlay.ts defines overlay stacking levels and panel dimensions used by JavaScript
// layout consumers. Stacking values correspond to global CSS variables and Tailwind
// extensions, with tests checking their agreement; nested surfaces and teleported
// controls use higher layers when they must appear above their owners.

// Z_INDEX orders chart overlays, navigation, dialogs, notifications, tours, and
// teleported controls; synchronize changes with global CSS and Tailwind stacking values.
export const Z_INDEX = {
  // Chart tooltips clear the sidebar while page-level tooltips stay below headers and dialogs.
  CHART_TOOLTIP: 45,
  SIDEBAR_OVERLAY: 30, // Mobile sidebar backdrop layer.
  SIDEBAR: 40, // Application sidebar content layer.
  HEADER: 50, // Application header content layer.
  MODAL: 50, // Base dialog content layer.
  MODAL_NESTED: 60, // Nested dialog content layer.
  TOOLTIP: 100, // Local tooltip content layer.
  ANNOUNCEMENT: 100, // Base announcement popup layer.
  ANNOUNCEMENT_RAISED: 120, // Raised announcement surface layer.
  ANNOUNCEMENT_TOP: 140, // Top announcement surface layer.
  MENU_OVERLAY: 9998, // Menu outside-click capture layer.
  TOAST: 9999, // Global notification content layer.
  ACTION_MENU: 9999, // Action menu content layer.
  TELEPORT_TOOLTIP: 9999, // Teleported tooltip content layer.
  HELP_TOOLTIP: 99999, // Help tooltip content layer.
  TOUR: 100000000, // Driver tour overlay layer.
  TELEPORT_DROPDOWN: 100000020, // Dropdown above tour layer.
} as const

// SELECT_PANEL_MAX_HEIGHT matches Select.vue's max-h-80 panel limit in pixels for
// JavaScript positioning and available-space calculations.
export const SELECT_PANEL_MAX_HEIGHT = 320

// MIN_COMFORTABLE_PANEL_HEIGHT is the pixel threshold below which panels prefer opening
// upward when the space above is larger.
export const MIN_COMFORTABLE_PANEL_HEIGHT = 240
