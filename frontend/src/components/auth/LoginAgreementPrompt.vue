<template>
  <div v-if="mode === 'checkbox' && documents.length > 0" class="px-0.5">
    <div class="flex items-start gap-2">
      <!--
        Anchor the missing-consent reminder to the checkbox so its focus target
        stays distinct from the wrapping legal-document links beside it.
      -->
      <HelpTooltip
        trigger="manual"
        :open="hintVisible && !accepted"
        tooltip-id="login-agreement-hint"
        :content="t('auth.agreementRequired')"
        :closable="false"
        width-class="w-72"
        class="mt-0.5 shrink-0"
        @update:open="emit('update:hintVisible', $event)"
      >
        <template #trigger>
          <input
            id="login-agreement-consent"
            ref="checkboxRef"
            type="checkbox"
            :checked="accepted"
            :aria-describedby="hintVisible && !accepted ? 'login-agreement-hint' : undefined"
            :aria-invalid="hintVisible && !accepted ? true : undefined"
            class="h-4 w-4 flex-shrink-0 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-900"
            @change="handleCheckboxChange"
          />
        </template>
      </HelpTooltip>
      <div class="min-w-0 flex-1">
        <p class="text-[13px] leading-5 text-gray-600 dark:text-dark-300">
          <label
            for="login-agreement-consent"
            class="cursor-pointer text-gray-700 dark:text-dark-200"
          >
            {{ t('legal.acceptedPrefix') }}
          </label>
          <template v-for="(doc, index) in documents" :key="doc.id || doc.title">
            <RouterLink
              :to="documentRoute(doc)"
              target="_blank"
              rel="noopener noreferrer"
              class="font-medium text-link"
            >
              {{ doc.title }}
            </RouterLink>
            <span v-if="index < documents.length - 1">、</span>
          </template>
        </p>
      </div>
    </div>
  </div>

  <div
    v-else-if="!accepted && documents.length > 0"
    class="rounded-surface border border-primary-100 bg-primary-50/70 p-3 text-sm text-primary-900 dark:border-primary-500/20 dark:bg-primary-500/10 dark:text-primary-100"
  >
    <div class="flex items-start gap-3">
      <Icon
        name="shield"
        size="sm"
        class="mt-0.5 flex-shrink-0 text-primary-600 dark:text-primary-300"
      />
      <div class="min-w-0 flex-1">
        <p class="font-medium">{{ t('legal.consentRequired') }}</p>
        <p class="mt-1 text-primary-700 dark:text-primary-200/80">
          {{ t('legal.disabledUntilAccepted') }}
        </p>
      </div>
      <button type="button" class="btn btn-primary btn-sm shrink-0" @click="emit('open')">
        {{ t('legal.viewTerms') }}
      </button>
    </div>
  </div>

  <Teleport to="body">
    <MotionTransition name="agreement-fade">
      <div
        v-if="dialogVisible"
        class="fixed inset-0 z-announcement-top flex items-center justify-center overflow-y-auto bg-gray-950/60 p-4 backdrop-blur-sm"
      >
        <div
          class="w-full max-w-[600px] overflow-hidden rounded-surface bg-white shadow-2xl ring-1 ring-black/10 dark:bg-dark-900 dark:ring-white/10 sm:rounded-dialog"
        >
          <div
            class="border-b border-gray-100 bg-white px-6 py-6 dark:border-dark-800 dark:bg-dark-900"
          >
            <div class="flex items-start gap-4">
              <span
                class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-control bg-primary-50 text-primary-700 ring-1 ring-primary-100 dark:bg-primary-500/10 dark:text-primary-300 dark:ring-primary-500/20"
              >
                <Icon name="shield" size="md" />
              </span>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <h2 class="text-xl font-bold tracking-normal text-gray-950 dark:text-white">
                    {{ t('legal.updateNotice') }}
                  </h2>
                  <span
                    v-if="updatedAt"
                    class="rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-800 dark:text-dark-300"
                  >
                    {{ updatedAt }}
                  </span>
                </div>
                <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
                  {{ t('legal.changedNotice', { date: updatedAt || t('legal.recently') }) }}
                </p>
              </div>
            </div>
          </div>

          <div class="max-h-[58vh] overflow-y-auto px-6 py-5">
            <div class="mb-3 flex items-center justify-between gap-3">
              <p class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('legal.documents') }}
              </p>
            </div>
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <RouterLink
                v-for="(doc, index) in documents"
                :key="doc.id || doc.title"
                :to="documentRoute(doc)"
                target="_blank"
                rel="noopener noreferrer"
                class="group text-link-area flex min-h-[72px] w-full items-center gap-3 rounded-surface border border-gray-200 bg-gray-50/70 px-4 py-3 text-left transition hover:-translate-y-0.5 hover:border-black/20 hover:bg-white hover:shadow-sm dark:border-dark-700 dark:bg-dark-800/70 dark:hover:border-primary-500/30 dark:hover:bg-dark-800"
              >
                <span
                  class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-control bg-white text-gray-700 ring-1 ring-gray-200 transition group-hover:bg-primary-50 group-hover:text-primary-700 group-hover:ring-primary-100 dark:bg-dark-900 dark:text-dark-200 dark:ring-dark-700 dark:group-hover:bg-primary-500/10 dark:group-hover:text-primary-200 dark:group-hover:ring-primary-500/20"
                >
                  <Icon :name="documentIcon(index, doc.title)" size="sm" />
                </span>
                <span class="min-w-0 flex-1">
                  <span class="text-link block truncate text-sm font-semibold">{{
                    doc.title
                  }}</span>
                </span>
                <span
                  class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full text-gray-400 transition group-hover:bg-primary-50 group-hover:text-primary-600 dark:group-hover:bg-primary-500/10 dark:group-hover:text-primary-300"
                >
                  <Icon name="externalLink" size="sm" />
                </span>
              </RouterLink>
            </div>
          </div>

          <div
            class="border-t border-gray-100 bg-gray-50/80 px-6 py-4 dark:border-dark-800 dark:bg-dark-950/60"
          >
            <div class="grid grid-cols-2 gap-3">
              <button type="button" class="btn btn-secondary" @click="emit('reject')">
                {{ t('legal.reject') }}
              </button>
              <button type="button" class="btn btn-primary" @click="emit('accept')">
                {{ t('legal.accept') }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </MotionTransition>
  </Teleport>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import type { LoginAgreementDocument } from '@/api/settings.types'

// LoginAgreementPrompt.vue renders checkbox or modal consent controls for
// configured legal documents. The parent owns acceptance and persistence, while
// this component filters blank titles, emits user decisions, and focuses the
// checkbox when an action needs consent.

// props supplies parent-owned configuration and draft input. emit reports
// authentication actions back to that owner, which decides whether to submit or
// navigate.
const props = withDefaults(
  defineProps<{
    // accepted is owned by the parent consent flow. This component emits decisions
    // and waits for updated props rather than persisting acceptance itself.
    accepted: boolean // parent consent decision

    // documents supplies the legal entries owned by the parent. Entries with blank
    // titles are filtered before rendering links or deciding whether the dialog
    // can appear.
    documents: LoginAgreementDocument[] // configured legal documents

    // mode selects checkbox presentation only for the literal checkbox value.
    // Other values normalize to modal presentation for the current document list.
    mode: 'modal' | 'checkbox' | string // consent presentation mode

    updatedAt?: string // displayed agreement update date

    visible: boolean // parent dialog visibility

    // hintVisible asks the component to focus and reveal the checkbox after
    // rendering. The reminder is displayed only while consent remains unaccepted.
    hintVisible?: boolean // checkbox reminder visibility
  }>(),
  {
    updatedAt: '',
  },
) // parent component inputs

const emit = defineEmits<{
  accept: [] // user accepted current agreement

  reject: [] // user rejected current agreement

  open: [] // user requested agreement dialog

  'update:hintVisible': [value: boolean] // updated checkbox reminder visibility
}>() // parent component event dispatcher

// t translates consent instructions and document actions for the active locale.
// checkboxRef provides the separate focus target used when the reminder becomes
// visible.
const { t } = useI18n() // translated authentication message resolver
// checkboxRef is the focus target when a blocked authentication action reveals the
// consent hint. Its DOM handle can be absent until checkbox mode renders.
const checkboxRef = ref<HTMLInputElement | null>(null) // consent reminder focus target
watch(
  () => props.hintVisible,
  async (visible) => {
    if (!visible) return
    await nextTick()
    // Provider buttons may sit below the consent control. Bring the matching
    // checkbox into view after rendering and focus it for keyboard acceptance.
    checkboxRef.value?.focus({ preventScroll: true })
    checkboxRef.value?.scrollIntoView?.({ block: 'nearest' })
  },
)

// dialogVisible requires both the parent visibility flag and a nonempty filtered
// document list. A dialog without a legal document does not open.
const dialogVisible = computed(() => props.visible && documents.value.length > 0) // document-backed agreement dialog visibility
// documents removes entries with blank titles before consent links render.
// updatedAt and accepted reflect the parent values used by the prompt copy and
// visibility.
const documents = computed(() => props.documents.filter((doc) => doc.title.trim())) // legal documents with nonblank titles
const updatedAt = computed(() => props.updatedAt || '') // displayed agreement update date
const accepted = computed(() => props.accepted) // parent-owned consent state
// mode chooses checkbox presentation only for the corresponding literal value.
// Other configured strings use the modal prompt so the template has two
// presentation branches.
const mode = computed(() => (props.mode === 'checkbox' ? 'checkbox' : 'modal')) // normalized consent presentation mode

// documentRoute builds the legal-document route using its identifier or title
// fallback. Documents without an identifier remain linkable by title, matching the
// filtered document list shown by the consent prompt.
function documentRoute(doc: LoginAgreementDocument) {
  return {
    name: 'LegalDocument',
    params: {
      documentId: doc.id || doc.title,
    },
  }
}

// handleCheckboxChange emits acceptance or rejection from the checkbox DOM state.
// The parent owns persistence and the accepted prop, so this handler reports user
// intent without changing either locally.
function handleCheckboxChange(event: Event): void {
  const checked = (event.target as HTMLInputElement).checked
  if (checked) {
    emit('accept')
  } else {
    emit('reject')
  }
}

// documentIcon selects a document symbol from title keywords and list position.
// Privacy and policy titles take priority, region titles use a globe, and the
// fourth remaining document uses a cog.
function documentIcon(index: number, title: string): 'document' | 'shield' | 'globe' | 'cog' {
  if (title.includes('政策') || title.includes('隐私')) {
    return 'shield'
  }
  if (title.includes('国家') || title.includes('地区')) {
    return 'globe'
  }
  if (index === 3) {
    return 'cog'
  }
  return 'document'
}
</script>

<style scoped>
.agreement-fade-enter-active,
.agreement-fade-leave-active {
  transition: opacity var(--motion-fast) var(--motion-ease);
}

.agreement-fade-enter-from,
.agreement-fade-leave-to {
  opacity: 0;
}

.agreement-fade-enter-active > div,
.agreement-fade-leave-active > div {
  transition:
    transform var(--motion-fast) var(--motion-ease),
    opacity var(--motion-fast) var(--motion-ease);
}

.agreement-fade-enter-from > div,
.agreement-fade-leave-to > div {
  opacity: 0;
  transform: translateY(8px) scale(0.98);
}
</style>
