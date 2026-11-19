import { beforeEach, describe, expect, it } from 'vitest'
import { usePersistedTableColumns } from '../usePersistedTableColumns'

/**
 * usePersistedTableColumns.spec.ts verifies hidden-column restoration, numeric
 * and string schema migrations, mandatory-column protection, and corrupted
 * storage fallback across table views.
 */

describe('usePersistedTableColumns', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  // Numeric version migrations append unseen default-hidden columns once while
  // stripping retired or mandatory keys and preserving saved choices.
  it('migrates numeric column versions and strips mandatory or removed keys', () => {
    localStorage.setItem('user-hidden-columns', JSON.stringify(['name', 'legacy_col', 'notes']))
    localStorage.setItem('user-column-settings-version', '2')

    const columns = usePersistedTableColumns({
      storageKey: 'user-hidden-columns',
      defaultHiddenColumns: ['notes', 'groups'],
      alwaysVisibleColumns: new Set(['name', 'actions']),
      removedColumns: new Set(['legacy_col']),
      migration: {
        versionKey: 'user-column-settings-version',
        currentVersion: 4,
        versionNewHiddenColumns: {
          2: ['usage_openai'],
          4: ['usage_qoder'],
        },
      },
    })
    columns.loadSavedColumns()

    expect([...columns.hiddenColumns]).toEqual(['notes', 'usage_qoder'])
    expect(localStorage.getItem('user-column-settings-version')).toBe('4')
    expect(columns.toggleColumn('name')).toBeNull()
    expect(columns.toggleColumn('notes')).toBe(true)
    expect(columns.isColumnVisible('notes')).toBe(true)
  })

  // String version markers add newly hidden columns once and do not re-hide them
  // after the user explicitly reveals them on a later visit.
  it('applies string-marker migrations once and falls back on malformed JSON', () => {
    localStorage.setItem('provider-hidden-columns', JSON.stringify(['proxy']))

    const firstVisit = usePersistedTableColumns({
      storageKey: 'provider-hidden-columns',
      defaultHiddenColumns: ['proxy', 'scheduler_score'],
      migration: {
        versionKey: 'provider-hidden-columns-version',
        currentVersion: 'scheduler-score-hidden-by-default',
        newHiddenColumns: ['scheduler_score'],
      },
    })
    firstVisit.loadSavedColumns()
    expect([...firstVisit.hiddenColumns]).toEqual(['proxy', 'scheduler_score'])
    firstVisit.toggleColumn('scheduler_score')

    const secondVisit = usePersistedTableColumns({
      storageKey: 'provider-hidden-columns',
      defaultHiddenColumns: ['proxy', 'scheduler_score'],
      migration: {
        versionKey: 'provider-hidden-columns-version',
        currentVersion: 'scheduler-score-hidden-by-default',
        newHiddenColumns: ['scheduler_score'],
      },
    })
    secondVisit.loadSavedColumns()
    expect(secondVisit.isColumnVisible('scheduler_score')).toBe(true)

    localStorage.setItem('provider-hidden-columns', '{bad-json')
    secondVisit.loadSavedColumns()
    expect([...secondVisit.hiddenColumns]).toEqual(['proxy', 'scheduler_score'])
  })
})
