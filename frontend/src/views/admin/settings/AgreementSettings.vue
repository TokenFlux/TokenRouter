<template>
  <SettingsCard
    :title="t('admin.settings.loginAgreement.title')"
    :description="t('admin.settings.loginAgreement.description')"
  >
    <template #actions>
      <Toggle
        v-model="form.login_agreement_enabled"
        size="md"
        :aria-label="t('admin.settings.loginAgreement.title')"
      />
    </template>
    <SettingsSection>
      <div>
        <p class="input-label">{{ t('admin.settings.loginAgreement.mode') }}</p>
        <SettingsSegmented
          v-model="form.login_agreement_mode"
          :options="loginAgreementModeOptions"
          :ariaLabel="t('admin.settings.loginAgreement.mode')"
        />
        <p class="input-hint">
          {{
            form.login_agreement_mode === 'checkbox'
              ? t('admin.settings.loginAgreement.modeCheckboxHint')
              : t('admin.settings.loginAgreement.modeModalHint')
          }}
        </p>
      </div>
      <SettingRow
        id="login-agreement-updated-at"
        field
        label-for="login-agreement-updated-at"
        :label="t('admin.settings.loginAgreement.updatedAt')"
        :hint="t('admin.settings.loginAgreement.updatedAtHint')"
      >
        <input
          id="login-agreement-updated-at"
          v-model="form.login_agreement_updated_at"
          type="date"
          class="input"
        />
      </SettingRow>
    </SettingsSection>

    <SettingsSection>
      <RuleListEditor
        :items="form.login_agreement_documents"
        title-style="section"
        :title="t('admin.settings.loginAgreement.documents')"
        :hint="t('admin.settings.loginAgreement.documentsHint')"
        :add-label="t('admin.settings.loginAgreement.addDocument')"
        variant="card"
        :item-label="
          (index) => t('admin.settings.loginAgreement.documentIndex', { index: index + 1 })
        "
        :min="form.login_agreement_enabled ? 1 : 0"
        test-id="login-agreement-documents"
        @add="addLoginAgreementDocument"
        @remove="removeLoginAgreementDocument"
      >
        <template #row="{ item: doc, index }">
          <div class="space-y-4">
            <div class="flex min-w-0 items-center gap-3">
              <span
                class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-control bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-dark-200"
              >
                <Icon
                  :name="
                    index === 1
                      ? 'shield'
                      : index === 2
                        ? 'globe'
                        : index === 3
                          ? 'cog'
                          : 'document'
                  "
                  size="sm"
                />
              </span>
              <div class="min-w-0">
                <p class="truncate text-sm font-semibold text-primary-900 dark:text-dark-50">
                  {{ doc.title || t('admin.settings.loginAgreement.untitledDocument') }}
                </p>
                <p class="truncate text-xs text-gray-500 dark:text-dark-400">
                  {{ loginAgreementRoutePath(doc, index) }}
                </p>
              </div>
            </div>
            <div class="grid gap-4 lg:grid-cols-2">
              <div>
                <label :for="`login-agreement-${index}-slug`" class="input-label">
                  {{ t('admin.settings.loginAgreement.documentSlug') }}
                </label>
                <!--
                  The input stores a slug suffix; the controller adds the fixed legal
                  route prefix when generating the document preview path.
                -->
                <div
                  class="flex min-h-9 overflow-hidden rounded-control border border-primary-900/10 bg-white transition duration-fast focus-within:border-black/30 dark:border-dark-600 dark:bg-dark-950 dark:focus-within:border-dark-400"
                >
                  <span
                    class="inline-flex flex-shrink-0 items-center border-r border-primary-900/10 bg-gray-50 px-3 text-sm text-gray-500 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-400"
                  >
                    /legal/
                  </span>
                  <input
                    :id="`login-agreement-${index}-slug`"
                    v-model="doc.id"
                    type="text"
                    class="min-w-0 flex-1 border-0 bg-transparent px-3 text-sm text-gray-900 outline-none placeholder:text-primary-900/45 focus:ring-0 dark:text-dark-50 dark:placeholder:text-dark-400"
                    placeholder="usage-policy"
                  />
                </div>
              </div>
            </div>
            <!--
              Publish title and Markdown source with their envelope so route previews,
              validation, and request serialization observe the same document edit.
            -->
            <LocalizedFieldsEditor
              :model-value="doc.localization"
              :source="{ title: doc.title, content_md: doc.content_md }"
              :fields="[
                {
                  key: 'title',
                  label: t('admin.settings.loginAgreement.documentTitle'),
                  placeholder: t('admin.settings.loginAgreement.documentTitlePlaceholder'),
                },
                {
                  key: 'content_md',
                  label: t('admin.settings.loginAgreement.documentContent'),
                  placeholder: t('admin.settings.loginAgreement.documentContentPlaceholder'),
                  multiline: true,
                  rows: 8,
                  inputClass: 'font-mono text-sm',
                },
              ]"
              @update:model-value="
                ($event) => {
                  doc.localization = $event
                  doc.title = $event.source.title
                  doc.content_md = $event.source.content_md
                }
              "
            />
          </div>
        </template>
      </RuleListEditor>
    </SettingsSection>
  </SettingsCard>
</template>

<script setup lang="ts">
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import LocalizedFieldsEditor from '@/components/common/LocalizedFieldsEditor.vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import Toggle from '@/components/common/Toggle.vue'
import type { useAgreementSettings, AgreementSettingsForm } from './useAgreementSettings'

/**
 * AgreementSettings.vue edits login confirmation modes and the documents served under
 * legal routes. The page-owned draft supplies the enabled switch, revision date, and
 * mutable document rows, while its controller supplies slug previews and addition or
 * removal actions. Editors remain mounted when the surrounding settings tab is hidden.
 *
 * Each localized document update publishes its envelope alongside title and Markdown
 * source fields, which the save validator later trims and checks. The slug input edits
 * only the path suffix; the controller supplies the legal prefix and normalization rules
 * for previews. Minimum row count follows the enabled switch, while final required-title
 * and duplicate-slug checks belong to bulk validation.
 */
// The parent supplies one controller and its shared draft for this mounted
// panel; template writes mutate that draft before the page save action runs.
const props = defineProps<{
  // controller retains page-scoped refs and actions; its closures target the
  // same form object provided below for this panel's editable controls.
  controller: ReturnType<typeof useAgreementSettings>
  // form is the parent-owned reactive draft; nested writes participate in the
  // bulk dirty tracker and remain visible across tab changes.
  form: AgreementSettingsForm
}>()
// Controller bindings retain their refs and closures so template events use
// the page state that hydration, validation, and shared saving also consume.
const {
  loginAgreementRoutePath,
  loginAgreementModeOptions,
  addLoginAgreementDocument,
  removeLoginAgreementDocument,
} = props.controller
// The draft alias keeps nested v-model writes on the parent-owned reactive
// object; the parent maintains its identity throughout this page lifetime.
const { form } = props
// Translation lookups are evaluated by the template so labels follow the
// active interface language while stored setting values retain their API meaning.
const { t } = useI18n()
</script>
