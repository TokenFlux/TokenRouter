// types.ts defines the column configuration consumed by DataTable and its column
// ordering composable. Column keys connect row values, named cell slots, sorting
// controls, and the browser preference that records movable column order.

// Column describes one rendered table column and its optional cell formatting
// behavior.
export interface Column {
  key: string // Row property and slot identifier.
  label: string // Displayed column header text.
  width?: string // Preferred CSS column width.
  sortable?: boolean // Header permits sorting interaction.
  class?: string // Header and cell classes.
  formatter?: (value: any, row: any) => string // Default cell text formatter.
}
