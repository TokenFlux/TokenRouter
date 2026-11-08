import {
  computed,
  inject,
  onBeforeUnmount,
  provide,
  shallowReactive,
  unref,
  type InjectionKey,
  type Ref,
} from 'vue'

// useSettingsSaveRegistry.ts aggregates independently owned settings sections for a shared
// save bar. Registered targets provide their own dirty state, persistence operation, and
// discard reload. The registry observes dirty flags but leaves each target responsible for
// updating its own clean snapshot after saving or reloading.
//
// Saving snapshots registration order and then rechecks each target's dirty flag
// immediately before invoking it. A false result records failure while allowing later
// saves, whereas a rejected promise interrupts the sequence. Discard selects dirty targets
// first and starts their reloads concurrently, waiting for every operation before
// reporting a failure. Component registrations are removed on
// unmount; hiding a mounted panel leaves registration active.

// SettingsSaveTarget exposes section-owned dirty state, persistence, and server reload
// operations.
export interface SettingsSaveTarget {
  // dirty remains owned by the target and is read again before each sequential save; the
  // registry never clears it itself.
  dirty: Ref<boolean> | boolean // section-owned pending edit indicator
  // save persists the target and returns its success flag. False allows later targets to
  // run, while rejection interrupts saving; the target records its own clean baseline.
  save: () => Promise<boolean> // section persistence result callback
  // discard reloads or replaces the section draft and updates its clean baseline. Dirty
  // targets start these operations concurrently. Failures propagate only after all
  // dispatched reloads settle, so callers can keep editing disabled throughout restoration.
  discard: () => Promise<void> // section draft reload callback
}

// SettingsSaveRegistry registers and removes named settings sections for page-level
// saving.
interface SettingsSaveRegistry {
  // register stores a target by stable key and replaces any previous target for that key.
  // Replacing an existing Map entry retains its registration order for saving.
  register: (key: string, target: SettingsSaveTarget) => void // named section registration callback
  // unregister removes a key from future aggregate operations. An already running save or
  // discard uses its captured target references, so removal does not cancel those
  // operations.
  unregister: (key: string) => void // named section removal callback
}

// settingsSaveRegistryKey connects a settings page provider with descendants registering
// their independently persisted sections during setup.
const settingsSaveRegistryKey: InjectionKey<SettingsSaveRegistry> = Symbol('settings-save-registry')

// createSettingsSaveRegistry creates a reactive collection of settings targets and
// aggregate save controls. Registering the same key replaces its target; the Map retains
// its insertion position. Each target owns its dirty flag and decides when persistence or
// reload makes it clean.
//
// Save operations iterate a copied target list sequentially and inspect dirty state at
// each turn. Discard chooses dirty targets up front and starts them together. The registry
// has no operation lock, so callers serialize overlapping save or discard actions.
export function createSettingsSaveRegistry() {
  // targets owns registered section references, and dirty observes their current flags
  // through the shallow reactive Map without changing target-owned draft state.
  const targets = shallowReactive(new Map<string, SettingsSaveTarget>())

  const dirty = computed(() => [...targets.values()].some((target) => unref(target.dirty)))

  // saveDirty awaits dirty targets in a snapshot of registration order and returns whether
  // every invoked save returned true. A false result does not stop later targets; a
  // rejected promise propagates and stops iteration.
  //
  // Dirty state is checked immediately before each save, allowing earlier work to affect
  // whether later targets run. Registering or removing targets during this operation does
  // not replace its copied target references. Targets remain responsible for recording the
  // correct saved snapshot.
  async function saveDirty(): Promise<boolean> {
    let ok = true
    // Copy targets before awaiting saves so registration changes do not alter this run's
    // order, while each dirty flag remains current.
    for (const target of [...targets.values()]) {
      if (!unref(target.dirty)) continue
      if (!(await target.save())) ok = false
    }
    return ok
  }

  // discardDirty starts reloads concurrently for targets dirty when the list is selected.
  // Each target updates its own draft and dirty baseline. After every reload settles,
  // the first failure in registration order is rethrown to the page.
  //
  // A quick failure must not release page controls while another target can still
  // overwrite its draft. Async callbacks also convert synchronous throws into rejected
  // promises, allowing all selected targets to start and preserving the same wait rule.
  async function discardDirty() {
    // Wait for successful and failed reloads together before the page clears its
    // activity flag; remaining callbacks must not race with newly enabled image editors.
    const results = await Promise.allSettled(
      [...targets.values()]
        .filter((target) => unref(target.dirty))
        .map(async (target) => target.discard()),
    )
    const failure = results.find((result) => result.status === 'rejected')
    if (failure) throw failure.reason
  }

  const registry: SettingsSaveRegistry = {
    register: (key, target) => targets.set(key, target),
    unregister: (key) => targets.delete(key),
  }

  return { dirty, saveDirty, discardDirty, registry }
}

// provideSettingsSaveRegistry creates the page registry and provides its registration API
// to descendants. Invoke it during setup; the returned aggregate controls drive the page's
// shared save and discard actions.
export function provideSettingsSaveRegistry() {
  const saveRegistry = createSettingsSaveRegistry()
  provide(settingsSaveRegistryKey, saveRegistry.registry)
  return saveRegistry
}

// useSettingsSaveTarget registers a section with the nearest settings registry during
// setup and unregisters its key before unmount. Missing providers are tolerated; callers
// use unique stable keys to avoid replacing another section.
export function useSettingsSaveTarget(key: string, target: SettingsSaveTarget) {
  const registry = inject(settingsSaveRegistryKey, null)
  if (!registry) return
  registry.register(key, target)
  onBeforeUnmount(() => registry.unregister(key))
}

export { settingsSaveRegistryKey }
