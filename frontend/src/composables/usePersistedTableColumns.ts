import { reactive } from 'vue'

/**
 * usePersistedTableColumns.ts manages per-table hidden-column preferences in
 * localStorage with schema-version migrations. Tables supply their existing
 * storage keys, mandatory visible columns, optional removed columns, and either
 * numeric version maps or string version markers so returning users retain saved
 * choices while newly added low-frequency columns default to hidden once.
 */

/**
 * PersistedTableColumnNumericMigration migrates integer-versioned preferences by
 * adding each unseen version's newly default-hidden column keys.
 */
export interface PersistedTableColumnNumericMigration {
  versionKey: string // Storage key holding the integer schema version.
  currentVersion: number // Target integer schema version (>= 1).
  versionNewHiddenColumns: Record<number, readonly string[]> // Added hidden keys per version.
}

/**
 * PersistedTableColumnStringMigration migrates string-marker preferences once
 * when the stored marker differs from currentVersion.
 */
export interface PersistedTableColumnStringMigration {
  versionKey: string // Storage key holding the string version marker.
  currentVersion: string // Target string version marker.
  newHiddenColumns: readonly string[] // Hidden keys added when marker differs.
}

/**
 * UsePersistedTableColumnsOptions configures storage keys, defaults, column
 * admission filters, and optional schema migrations for one table view.
 */
export interface UsePersistedTableColumnsOptions {
  storageKey: string // localStorage key storing JSON string[] of hidden columns.
  defaultHiddenColumns: readonly string[] // Initial hidden keys when storage is absent or unreadable.
  alwaysVisibleColumns?: ReadonlySet<string> // Mandatory columns excluded from hiding.
  removedColumns?: ReadonlySet<string> // Retired keys stripped during load.
  getValidColumnKeys?: () => ReadonlySet<string> // Dynamic allowlist of hideable column keys.
  migration?: PersistedTableColumnNumericMigration | PersistedTableColumnStringMigration // Optional schema migration.
}

/**
 * usePersistedTableColumns creates a reactive hiddenColumns set and storage
 * synchronization helpers for a customizable table. Hidden keys are written
 * before advancing any version marker so an interrupted write leaves migrations
 * eligible on the next visit, while storage or JSON failures fall back to
 * defaultHiddenColumns without breaking table rendering.
 */
export function usePersistedTableColumns(options: UsePersistedTableColumnsOptions) {
  const hiddenColumns = reactive<Set<string>>(new Set())

  /**
   * isAllowedHiddenKey reports whether key may be stored as hidden under the
   * configured mandatory, retired, and dynamic column constraints.
   */
  const isAllowedHiddenKey = (
    key: unknown,
    validKeys: ReadonlySet<string> | null,
  ): key is string => {
    if (typeof key !== 'string') return false
    if (options.alwaysVisibleColumns?.has(key)) return false
    if (options.removedColumns?.has(key)) return false
    if (validKeys && !validKeys.has(key)) return false
    return true
  }

  /**
   * writeVersionMarker records the configured migration version in localStorage
   * when migration metadata is present.
   */
  const writeVersionMarker = () => {
    if (!options.migration) return
    localStorage.setItem(options.migration.versionKey, String(options.migration.currentVersion))
  }

  /**
   * saveColumnsToStorage persists allowed hidden-column keys before advancing
   * any configured version marker.
   */
  const saveColumnsToStorage = () => {
    try {
      const validKeys = options.getValidColumnKeys ? options.getValidColumnKeys() : null
      const keys = [...hiddenColumns].filter((key) => isAllowedHiddenKey(key, validKeys))
      localStorage.setItem(options.storageKey, JSON.stringify(keys))
      writeVersionMarker()
    } catch (error) {
      console.error(`Failed to save table columns (${options.storageKey}):`, error)
    }
  }

  /**
   * loadSavedColumns restores hidden columns from localStorage and applies any
   * pending numeric or string version migrations.
   */
  const loadSavedColumns = () => {
    hiddenColumns.clear()
    const validKeys = options.getValidColumnKeys ? options.getValidColumnKeys() : null
    try {
      const saved = localStorage.getItem(options.storageKey)
      if (saved) {
        const parsed = JSON.parse(saved)
        if (Array.isArray(parsed)) {
          parsed
            .filter((key) => isAllowedHiddenKey(key, validKeys))
            .forEach((key) => hiddenColumns.add(key))
        }

        const migration = options.migration
        if (migration) {
          if ('versionNewHiddenColumns' in migration) {
            const rawVersion = Number(localStorage.getItem(migration.versionKey) ?? '1')
            const storedVersion =
              Number.isSafeInteger(rawVersion) && rawVersion >= 1 ? rawVersion : 1
            if (storedVersion < migration.currentVersion) {
              let mutated = false
              for (let v = storedVersion + 1; v <= migration.currentVersion; v++) {
                for (const key of migration.versionNewHiddenColumns[v] ?? []) {
                  if (isAllowedHiddenKey(key, validKeys) && !hiddenColumns.has(key)) {
                    hiddenColumns.add(key)
                    mutated = true
                  }
                }
              }
              if (mutated) {
                saveColumnsToStorage()
              } else {
                writeVersionMarker()
              }
            }
          } else if (localStorage.getItem(migration.versionKey) !== migration.currentVersion) {
            for (const key of migration.newHiddenColumns) {
              if (isAllowedHiddenKey(key, validKeys)) {
                hiddenColumns.add(key)
              }
            }
            saveColumnsToStorage()
          }
        }
      } else {
        for (const key of options.defaultHiddenColumns) {
          if (isAllowedHiddenKey(key, validKeys)) {
            hiddenColumns.add(key)
          }
        }
        if (options.getValidColumnKeys) {
          saveColumnsToStorage()
        } else {
          writeVersionMarker()
        }
      }
    } catch (error) {
      console.error(`Failed to load table columns (${options.storageKey}):`, error)
      for (const key of options.defaultHiddenColumns) {
        if (!options.alwaysVisibleColumns?.has(key) && !options.removedColumns?.has(key)) {
          hiddenColumns.add(key)
        }
      }
    }
  }

  /**
   * toggleColumn flips visibility for an allowed column, persists the updated
   * preference, and returns whether the column transitioned from hidden to
   * visible (or null when the column cannot be toggled).
   */
  const toggleColumn = (key: string): boolean | null => {
    const validKeys = options.getValidColumnKeys ? options.getValidColumnKeys() : null
    if (!isAllowedHiddenKey(key, validKeys)) return null
    const wasHidden = hiddenColumns.has(key)
    if (wasHidden) {
      hiddenColumns.delete(key)
    } else {
      hiddenColumns.add(key)
    }
    saveColumnsToStorage()
    return wasHidden
  }

  /**
   * isColumnVisible reports whether key is mandatory or absent from hiddenColumns.
   */
  const isColumnVisible = (key: string): boolean => {
    if (options.alwaysVisibleColumns?.has(key)) return true
    return !hiddenColumns.has(key)
  }

  return {
    hiddenColumns,
    loadSavedColumns,
    saveColumnsToStorage,
    toggleColumn,
    isColumnVisible,
  }
}
