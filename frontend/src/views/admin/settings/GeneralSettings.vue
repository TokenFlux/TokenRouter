<template>
  <PreAggregationSettings />

  <!-- Usage ranking controls. -->
  <SettingsCard
    :title="t('admin.settings.usageRanking.title')"
    :description="t('admin.settings.usageRanking.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="usage-ranking-enabled"
        v-model="form.usage_ranking_enabled"
        :label="t('admin.settings.usageRanking.enabled')"
        :hint="t('admin.settings.usageRanking.enabledHint')"
      />
      <Collapse :open="form.usage_ranking_enabled">
        <SettingsSubpanel>
          <SettingRow
            id="usage-ranking-sort-by"
            field
            :label="t('admin.settings.usageRanking.sortBy')"
            :hint="t('admin.settings.usageRanking.sortByHint')"
          >
            <Select
              v-model="form.usage_ranking_sort_by"
              :options="usageRankingSortOptions"
              :aria-label="t('admin.settings.usageRanking.sortBy')"
            />
          </SettingRow>
          <SettingRow
            id="usage-ranking-limit"
            field
            label-for="usage-ranking-limit"
            :label="t('admin.settings.usageRanking.limit')"
            :hint="t('admin.settings.usageRanking.limitHint')"
          >
            <input
              id="usage-ranking-limit"
              v-model.number="form.usage_ranking_limit"
              type="number"
              min="1"
              max="100"
              step="1"
              class="input"
            />
          </SettingRow>
          <!--
            The controller enables the selected ranking metric; disabling its toggle keeps
            users able to see the basis of ordering.
          -->
          <SettingsSection
            :title="t('admin.settings.usageRanking.fields')"
            :hint="t('admin.settings.usageRanking.fieldsHint')"
          >
            <SettingToggleRow
              id="usage-ranking-show-total-tokens"
              v-model="form.usage_ranking_show_total_tokens"
              :label="t('admin.settings.usageRanking.totalTokens')"
              :disabled="form.usage_ranking_sort_by === 'total_tokens'"
            />
            <SettingToggleRow
              id="usage-ranking-show-requests"
              v-model="form.usage_ranking_show_requests"
              :label="t('admin.settings.usageRanking.requests')"
              :disabled="form.usage_ranking_sort_by === 'requests'"
            />
            <SettingToggleRow
              id="usage-ranking-show-actual-cost"
              v-model="form.usage_ranking_show_actual_cost"
              :label="t('admin.settings.usageRanking.actualCost')"
              :disabled="form.usage_ranking_sort_by === 'actual_cost'"
            />
          </SettingsSection>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <!-- Balance display controls. -->
  <SettingsCard
    :title="t('admin.settings.balanceDisplay.title')"
    :description="t('admin.settings.balanceDisplay.description')"
  >
    <SettingsSection>
      <div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_280px]">
        <div class="grid content-start gap-4 md:grid-cols-2">
          <div>
            <LocalizedEditor
              :label="t('admin.settings.balanceDisplay.unitName')"
              :placeholder="t('admin.settings.balanceDisplay.unitNamePlaceholder')"
              v-model="form.localized_settings.balance_unit_name"
              :default-source-locale="getLocale()"
            />
            <p class="input-hint">{{ t('admin.settings.balanceDisplay.unitNameHint') }}</p>
          </div>
          <div>
            <label for="balance-unit-symbol" class="input-label">
              {{ t('admin.settings.balanceDisplay.unitSymbol') }}
            </label>
            <input
              id="balance-unit-symbol"
              v-model="form.balance_unit_symbol"
              type="text"
              class="input"
              :placeholder="t('admin.settings.balanceDisplay.unitSymbolPlaceholder')"
            />
            <p class="input-hint">
              {{ t('admin.settings.balanceDisplay.unitSymbolHint') }}
            </p>
          </div>
          <div>
            <label for="reasoning-point-rmb-unit-price" class="input-label">
              {{
                t('admin.settings.balanceDisplay.reasoningPointRmbUnitPrice', {
                  unitName: previewBalanceUnitName,
                })
              }}
            </label>
            <input
              id="reasoning-point-rmb-unit-price"
              v-model.number="form.reasoning_point_rmb_unit_price"
              type="number"
              min="0"
              step="0.0001"
              class="input"
              :placeholder="
                t('admin.settings.balanceDisplay.reasoningPointRmbUnitPricePlaceholder')
              "
            />
            <p class="input-hint">
              {{
                t('admin.settings.balanceDisplay.reasoningPointRmbUnitPriceHint', {
                  unitName: previewBalanceUnitName,
                })
              }}
            </p>
          </div>
          <div>
            <label for="usd-exchange-rate" class="input-label">
              {{ t('admin.settings.balanceDisplay.usdExchangeRate') }}
            </label>
            <input
              id="usd-exchange-rate"
              v-model.number="form.usd_exchange_rate"
              type="number"
              min="0"
              step="0.0001"
              class="input"
              :placeholder="t('admin.settings.balanceDisplay.usdExchangeRatePlaceholder')"
            />
            <p class="input-hint">
              {{ t('admin.settings.balanceDisplay.usdExchangeRateHint') }}
            </p>
          </div>
          <div class="md:col-span-2">
            <p class="input-label">{{ t('admin.settings.balanceDisplay.iconSvg') }}</p>
            <ImageUpload
              :disabled="imagesDisabled"
              v-model="form.balance_icon_svg"
              mode="svg"
              :upload-label="t('admin.settings.balanceDisplay.uploadSvg')"
              :remove-label="t('admin.settings.balanceDisplay.removeSvg')"
              :hint="t('admin.settings.balanceDisplay.iconHint')"
            />
          </div>
        </div>

        <!--
          Preview units come from the draft so administrators can inspect the balance
          display before saving those settings.
        -->
        <SettingsSubpanel class="self-start">
          <p class="input-label mb-0">
            {{ t('admin.settings.balanceDisplay.previewLabel') }}
          </p>
          <div
            class="rounded-surface border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-900"
          >
            <div class="flex items-center gap-3">
              <div
                class="flex h-11 w-11 items-center justify-center rounded-surface bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300"
              >
                <BalanceIcon
                  :svg="form.balance_icon_svg"
                  :use-global-fallback="false"
                  class="h-5 w-5"
                />
              </div>
              <div>
                <p class="text-xs text-gray-500 dark:text-dark-400">
                  {{
                    t('admin.settings.balanceDisplay.previewUnit', {
                      unitName: previewBalanceUnitName,
                    })
                  }}
                </p>
                <p class="text-xl font-semibold text-gray-900 dark:text-dark-50">
                  {{ previewBalanceAmount }}
                </p>
              </div>
            </div>
          </div>
          <p class="input-hint">{{ t('admin.settings.balanceDisplay.previewHint') }}</p>
        </SettingsSubpanel>
      </div>
    </SettingsSection>
  </SettingsCard>

  <!-- Marketplace availability windows. -->
  <SettingsCard
    :title="t('admin.settings.marketplaceAvailability.title')"
    :description="t('admin.settings.marketplaceAvailability.description')"
  >
    <SettingsSection>
      <SettingRow
        id="marketplace-availability-window-days"
        field
        label-for="marketplace-availability-window-days"
        :label="t('admin.settings.marketplaceAvailability.windowDays')"
        :hint="
          t('admin.settings.marketplaceAvailability.windowDaysHint', {
            min: marketplaceAvailabilityWindowDaysMin,
            max: marketplaceAvailabilityWindowDaysMax,
          })
        "
      >
        <input
          id="marketplace-availability-window-days"
          v-model.number="form.marketplace_availability_window_days"
          type="number"
          :min="marketplaceAvailabilityWindowDaysMin"
          :max="marketplaceAvailabilityWindowDaysMax"
          step="1"
          class="input"
        />
      </SettingRow>
      <SettingRow
        id="marketplace-availability-bucket-minutes"
        field
        label-for="marketplace-availability-bucket-minutes"
        :label="t('admin.settings.marketplaceAvailability.bucketMinutes')"
        :hint="
          t('admin.settings.marketplaceAvailability.bucketMinutesHint', {
            min: marketplaceAvailabilityBucketMinutesMin,
            max: marketplaceAvailabilityBucketMinutesMax,
          })
        "
      >
        <input
          id="marketplace-availability-bucket-minutes"
          v-model.number="form.marketplace_availability_bucket_minutes"
          type="number"
          :min="marketplaceAvailabilityBucketMinutesMin"
          :max="marketplaceAvailabilityBucketMinutesMax"
          step="1"
          class="input"
        />
      </SettingRow>
      <p class="input-hint">
        {{ t('admin.settings.marketplaceAvailability.bucketLimitHint') }}
      </p>
    </SettingsSection>
  </SettingsCard>

  <!-- Site presentation and navigation settings. -->
  <SettingsCard
    :title="t('admin.settings.site.title')"
    :description="t('admin.settings.site.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="backend-mode-enabled"
        v-model="form.backend_mode_enabled"
        :label="t('admin.settings.site.backendMode')"
        :hint="t('admin.settings.site.backendModeDescription')"
      />
    </SettingsSection>

    <SettingsSection
      :title="t('admin.settings.site.siteCopyTitle')"
      :hint="t('admin.settings.site.siteCopyDescription')"
    >
      <div>
        <label class="input-label">{{ t('localization.defaultLanguage') }}</label>
        <Select
          v-model="form.default_locale"
          :options="availableLocales.map((item) => ({ value: item.code, label: item.name }))"
        />
      </div>
      <div v-for="key in ['site_name', 'site_title', 'site_subtitle']" :key="key">
        <LocalizedEditor
          :label="t(`localization.fields.${key}`)"
          v-model="form.site_texts[key]"
          :default-source-locale="getLocale()"
        />
      </div>
      <div>
        <p class="input-label">{{ t('admin.settings.site.siteLogo') }}</p>
        <!-- The logo upload limit is expressed in bytes, with 300 KiB converted for ImageUpload. -->
        <ImageUpload
          :disabled="imagesDisabled"
          v-model="form.site_logo"
          mode="image"
          :upload-label="t('admin.settings.site.uploadImage')"
          :remove-label="t('admin.settings.site.remove')"
          :hint="t('admin.settings.site.logoHint')"
          :max-size="300 * 1024"
        />
      </div>
    </SettingsSection>

    <SettingsSection
      :title="t('admin.settings.site.socialSharing')"
      :hint="t('admin.settings.site.socialSharingHint')"
    >
      <LocalizedEditor
        v-model="form.site_texts.site_share_title"
        :label="t('localization.fields.site_share_title')"
        :default-source-locale="getLocale()"
      />
      <p class="input-hint">{{ t('admin.settings.site.shareTitleHint') }}</p>
      <LocalizedEditor
        v-model="form.site_texts.site_share_description"
        :label="t('localization.fields.site_share_description')"
        :default-source-locale="getLocale()"
        :rows="3"
      />
      <p class="input-hint">{{ t('admin.settings.site.shareDescriptionHint') }}</p>
      <div>
        <p class="input-label">{{ t('admin.settings.site.shareCover') }}</p>
        <ImageUpload
          :disabled="imagesDisabled"
          v-model="form.site_share_image"
          mode="image"
          wide
          :allowed-types="['image/png', 'image/jpeg', 'image/webp']"
          :max-size="2 * 1024 * 1024"
          :max-dimension="4096"
          :hint="t('admin.settings.site.shareCoverHint')"
        />
      </div>
    </SettingsSection>

    <SettingsSection>
      <div>
        <label for="api-base-url" class="input-label">{{
          t('admin.settings.site.apiBaseUrl')
        }}</label>
        <input
          id="api-base-url"
          v-model="form.api_base_url"
          type="text"
          class="input font-mono text-sm"
          :placeholder="t('admin.settings.site.apiBaseUrlPlaceholder')"
        />
        <p class="input-hint">{{ t('admin.settings.site.apiBaseUrlHint') }}</p>
      </div>
      <div class="grid gap-4 md:grid-cols-2">
        <div>
          <LocalizedEditor
            :label="t('admin.settings.site.contactInfo')"
            :placeholder="t('admin.settings.site.contactInfoPlaceholder')"
            v-model="form.site_texts.contact_info"
            :default-source-locale="getLocale()"
          />
          <p class="input-hint">{{ t('admin.settings.site.contactInfoHint') }}</p>
        </div>
        <div>
          <LocalizedEditor
            :label="t('admin.settings.site.docUrl')"
            :placeholder="t('admin.settings.site.docUrlPlaceholder')"
            input-class="font-mono text-sm"
            v-model="form.site_texts.doc_url"
            :default-source-locale="getLocale()"
          />
          <p class="input-hint">{{ t('admin.settings.site.docUrlHint') }}</p>
        </div>
      </div>
    </SettingsSection>

    <SettingsSection>
      <RuleListEditor
        :items="form.custom_endpoints"
        variant="card"
        title-style="section"
        :title="t('admin.settings.site.customEndpoints.title')"
        :hint="t('admin.settings.site.customEndpoints.description')"
        :item-label="
          (index) => t('admin.settings.site.customEndpoints.itemLabel', { n: index + 1 })
        "
        :add-label="t('admin.settings.site.customEndpoints.add')"
        test-id="custom-endpoints"
        @add="addEndpoint"
        @remove="removeEndpoint"
      >
        <template #row="{ item: ep, index: epIndex }">
          <div class="grid gap-4 sm:grid-cols-2">
            <!--
              Publish localized sources with their envelopes so endpoint previews and bulk
              serialization read the same edited name and description.
            -->
            <LocalizedFieldsEditor
              class="sm:col-span-2"
              layout="grid gap-4 sm:grid-cols-2"
              :model-value="ep.localization"
              :source="{ name: ep.name, description: ep.description }"
              :fields="[
                {
                  key: 'name',
                  label: t('admin.settings.site.customEndpoints.name'),
                  placeholder: t('admin.settings.site.customEndpoints.namePlaceholder'),
                },
                {
                  key: 'description',
                  label: t('admin.settings.site.customEndpoints.descriptionLabel'),
                  placeholder: t('admin.settings.site.customEndpoints.descriptionPlaceholder'),
                },
              ]"
              @update:model-value="
                ($event) => {
                  ep.localization = $event
                  ep.name = $event.source.name
                  ep.description = $event.source.description
                }
              "
            />
            <div>
              <label :for="`custom-endpoint-${epIndex}-url`" class="input-label">
                {{ t('admin.settings.site.customEndpoints.endpointUrl') }}
              </label>
              <input
                :id="`custom-endpoint-${epIndex}-url`"
                v-model="ep.endpoint"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.site.customEndpoints.endpointUrlPlaceholder')"
              />
            </div>
          </div>
        </template>
      </RuleListEditor>
    </SettingsSection>

    <SettingsSection
      :title="t('admin.settings.site.tablePreferencesTitle')"
      :hint="t('admin.settings.site.tablePreferencesDescription')"
    >
      <SettingRow
        id="table-default-page-size"
        field
        label-for="table-default-page-size"
        :label="t('admin.settings.site.tableDefaultPageSize')"
        :hint="t('admin.settings.site.tableDefaultPageSizeHint')"
      >
        <input
          id="table-default-page-size"
          v-model.number="form.table_default_page_size"
          type="number"
          min="5"
          max="1000"
          step="1"
          class="input"
        />
      </SettingRow>
      <div>
        <label for="table-page-size-options" class="input-label">
          {{ t('admin.settings.site.tablePageSizeOptions') }}
        </label>
        <input
          id="table-page-size-options"
          v-model="tablePageSizeOptionsInput"
          type="text"
          class="input font-mono text-sm"
          :placeholder="t('admin.settings.site.tablePageSizeOptionsPlaceholder')"
        />
        <p class="input-hint">{{ t('admin.settings.site.tablePageSizeOptionsHint') }}</p>
      </div>
    </SettingsSection>

    <SettingsSection>
      <div>
        <LocalizedEditor
          :label="t('admin.settings.site.homeContent')"
          :placeholder="t('admin.settings.site.homeContentPlaceholder')"
          input-class="font-mono text-sm"
          v-model="form.site_texts.home_content"
          :default-source-locale="getLocale()"
          :rows="6"
        />
        <p class="input-hint">{{ t('admin.settings.site.homeContentHint') }}</p>
      </div>
      <!--
        External homepage URLs render in an iframe; a remote Content Security Policy can
        block embedding and leave that content unavailable.
      -->
      <SettingsNotice tone="warning">
        {{ t('admin.settings.site.homeContentIframeWarning') }}
      </SettingsNotice>
      <SettingToggleRow
        id="hide-ccs-import-button"
        v-model="form.hide_ccs_import_button"
        :label="t('admin.settings.site.hideCcsImportButton')"
        :hint="t('admin.settings.site.hideCcsImportButtonHint')"
      />
    </SettingsSection>
  </SettingsCard>

  <!-- Custom menu rows. -->
  <SettingsCard
    :title="t('admin.settings.customMenu.title')"
    :description="t('admin.settings.customMenu.description')"
  >
    <SettingsSection>
      <RuleListEditor
        :items="form.custom_menu_items"
        variant="card"
        :item-label="(index) => t('admin.settings.customMenu.itemLabel', { n: index + 1 })"
        :add-label="t('admin.settings.customMenu.add')"
        :remove-label="t('admin.settings.customMenu.remove')"
        add-placement="footer"
        reorderable
        test-id="custom-menu-items"
        @add="addMenuItem"
        @remove="removeMenuItem"
        @move="(from, to) => moveMenuItem(from, to > from ? 1 : -1)"
      >
        <template #row="{ item, index: menuIndex }">
          <div class="grid gap-4 sm:grid-cols-2">
            <!--
              Administrator menu rows edit plain source fields, while user-facing rows
              publish their localization envelope together with label and URL.
            -->
            <template v-if="item.visibility === 'admin'">
              <div>
                <label :for="`custom-menu-${menuIndex}-label`" class="input-label">
                  {{ t('admin.settings.customMenu.name') }}
                </label>
                <input
                  :id="`custom-menu-${menuIndex}-label`"
                  v-model="item.label"
                  type="text"
                  class="input"
                  :placeholder="t('admin.settings.customMenu.namePlaceholder')"
                />
              </div>
              <div>
                <label :for="`custom-menu-${menuIndex}-url`" class="input-label">
                  {{ t('admin.settings.customMenu.url') }}
                </label>
                <input
                  :id="`custom-menu-${menuIndex}-url`"
                  v-model="item.url"
                  type="url"
                  class="input font-mono text-sm"
                  :placeholder="t('admin.settings.customMenu.urlPlaceholder')"
                />
              </div>
            </template>
            <LocalizedFieldsEditor
              v-else
              class="sm:col-span-2"
              layout="grid gap-4 sm:grid-cols-2"
              :model-value="item.localization"
              :source="{ label: item.label, url: item.url }"
              :fields="[
                {
                  key: 'label',
                  label: t('admin.settings.customMenu.name'),
                  placeholder: t('admin.settings.customMenu.namePlaceholder'),
                },
                {
                  key: 'url',
                  label: t('admin.settings.customMenu.url'),
                  placeholder: t('admin.settings.customMenu.urlPlaceholder'),
                  url: true,
                  inputClass: 'font-mono text-sm',
                },
              ]"
              @update:model-value="
                ($event) => {
                  item.localization = $event
                  item.label = $event.source.label
                  item.url = $event.source.url
                }
              "
            />
            <div>
              <label :for="`custom-menu-${menuIndex}-visibility`" class="input-label">
                {{ t('admin.settings.customMenu.visibility') }}
              </label>
              <Select
                :id="`custom-menu-${menuIndex}-visibility`"
                v-model="item.visibility"
                :options="customMenuVisibilityOptions"
              />
            </div>
            <div class="sm:col-span-2">
              <p class="input-label">{{ t('admin.settings.customMenu.iconSvg') }}</p>
              <ImageUpload
                :disabled="imagesDisabled"
                :model-value="item.icon_svg"
                mode="svg"
                size="sm"
                :upload-label="t('admin.settings.customMenu.uploadSvg')"
                :remove-label="t('admin.settings.customMenu.removeSvg')"
                @update:model-value="(v: string) => (item.icon_svg = v)"
              />
            </div>
          </div>
        </template>
      </RuleListEditor>
    </SettingsSection>
  </SettingsCard>

  <!-- Featured homepage model ordering. -->
  <SettingsCard
    :title="t('admin.settings.homeFeaturedModels.title')"
    :description="t('admin.settings.homeFeaturedModels.description')"
  >
    <template v-if="form.home_featured_models.length > 0" #actions>
      <button
        type="button"
        class="btn btn-secondary btn-sm h-9 shrink-0"
        @click="form.home_featured_models = []"
      >
        {{ t('admin.settings.homeFeaturedModels.clear') }}
      </button>
    </template>
    <SettingsSection>
      <RuleListEditor
        :items="form.home_featured_models"
        :add-label="t('admin.settings.homeFeaturedModels.add')"
        :remove-label="t('admin.settings.homeFeaturedModels.remove')"
        :max="homeFeaturedModelsMax"
        :animated="false"
        add-placement="footer"
        reorderable
        test-id="home-featured-models"
        @add="form.home_featured_models.push('')"
        @remove="removeHomeFeaturedModel"
        @move="(from, to) => moveHomeFeaturedModel(from, to > from ? 1 : -1)"
      >
        <template #row="{ index: mIndex }">
          <Select
            v-model="form.home_featured_models[mIndex]"
            :options="homeFeaturedModelOptions"
            searchable
            class="min-w-0"
            :aria-label="t('admin.settings.homeFeaturedModels.select')"
            :placeholder="t('admin.settings.homeFeaturedModels.select')"
          />
        </template>
      </RuleListEditor>
      <p v-if="homeFeaturedModelOptions.length === 0" class="input-hint">
        {{ t('admin.settings.homeFeaturedModels.empty') }}
      </p>
    </SettingsSection>
  </SettingsCard>

  <!-- Homepage footer groups and links. -->
  <SettingsCard
    :title="t('admin.settings.homeFooter.title')"
    :description="t('admin.settings.homeFooter.description')"
  >
    <template #actions>
      <button
        type="button"
        class="btn btn-secondary btn-sm h-9 shrink-0"
        @click="applyDefaultFooterLinks"
      >
        {{ t('admin.settings.homeFooter.useDefault') }}
      </button>
    </template>
    <SettingsSection>
      <RuleListEditor
        :items="form.footer_links"
        variant="card"
        :item-label="(index) => t('admin.settings.homeFooter.groupIndex', { index: index + 1 })"
        :add-label="t('admin.settings.homeFooter.addGroup')"
        :remove-label="t('admin.settings.homeFooter.removeGroup')"
        add-placement="footer"
        reorderable
        test-id="footer-groups"
        @add="addFooterGroup"
        @remove="removeFooterGroup"
        @move="(from, to) => moveFooterGroup(from, to > from ? 1 : -1)"
      >
        <template #row="{ item: group, index: gIndex }">
          <div class="space-y-4">
            <!--
              Footer title edits update the plain source alongside the envelope; the save
              normalizer uses that source when accepting groups.
            -->
            <!--
              A footer group without localization starts from its plain title, allowing existing
              source-only groups to open the same editor as localized groups.
            -->
            <LocalizedEditor
              class="sm:max-w-xs"
              :label="t('admin.settings.homeFooter.groupTitle')"
              :placeholder="t('admin.settings.homeFooter.groupTitlePlaceholder')"
              :model-value="group.localization || originalContent(group.title)"
              @update:model-value="
                ($event) => {
                  group.localization = $event
                  group.title = $event.source
                }
              "
            />
            <RuleListEditor
              :items="group.links"
              :add-label="t('admin.settings.homeFooter.addLink')"
              :remove-label="t('admin.settings.homeFooter.removeLink')"
              add-placement="footer"
              :test-id="`footer-links-${gIndex}`"
              @add="addFooterLink(group)"
              @remove="group.links.splice($event, 1)"
            >
              <template #row="{ item: link }">
                <!--
                  Link edits publish label and URL together with localization; incomplete
                  source pairs are removed by footer normalization before saving.
                -->
                <LocalizedFieldsEditor
                  layout="grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
                  :model-value="link.localization"
                  :source="{ label: link.label, url: link.url }"
                  :fields="[
                    {
                      key: 'label',
                      label: t('admin.settings.homeFooter.linkLabel'),
                      placeholder: t('admin.settings.homeFooter.linkLabel'),
                    },
                    {
                      key: 'url',
                      label: t('admin.settings.homeFooter.linkUrl'),
                      placeholder: t('admin.settings.homeFooter.linkUrlPlaceholder'),
                      inputClass: 'min-w-0 font-mono text-sm',
                    },
                  ]"
                  @update:model-value="
                    ($event) => {
                      link.localization = $event
                      link.label = $event.source.label
                      link.url = $event.source.url
                    }
                  "
                />
              </template>
            </RuleListEditor>
          </div>
        </template>
      </RuleListEditor>
    </SettingsSection>
    <SettingsSection>
      <div>
        <LocalizedEditor
          :label="t('admin.settings.homeFooter.extraText')"
          :placeholder="t('admin.settings.homeFooter.extraTextPlaceholder')"
          v-model="form.site_texts.footer_text"
          :default-source-locale="getLocale()"
          :rows="2"
        />
      </div>
    </SettingsSection>
  </SettingsCard>
</template>

<script setup lang="ts">
import {
  marketplaceAvailabilityWindowDaysMin,
  marketplaceAvailabilityWindowDaysMax,
  marketplaceAvailabilityBucketMinutesMin,
  marketplaceAvailabilityBucketMinutesMax,
} from './settingsDefaults'
import Collapse from '@/components/common/Collapse.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSubpanel from '@/components/common/settings/SettingsSubpanel.vue'
import LocalizedFieldsEditor from '@/components/common/LocalizedFieldsEditor.vue'
import LocalizedEditor from '@/components/common/LocalizedEditor.vue'
import { originalContent } from '@/i18n/content'
import { availableLocales, getLocale } from '@/i18n'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import ImageUpload from '@/components/common/ImageUpload.vue'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import PreAggregationSettings from '@/views/admin/settings/PreAggregationSettings.vue'
import type { useGeneralSettings, GeneralSettingsForm } from './useGeneralSettings'

/**
 * GeneralSettings.vue renders site presentation, public navigation, featured models, and
 * footer editors against the page-owned settings draft. The controller supplies list
 * actions, marketplace choices, and balance previews, while the mounted panel binds each
 * editor to the same objects used during bulk saving. PreAggregationSettings registers its
 * separate persistence target within this panel.
 *
 * Localized row editors write both their localization envelope and source fields together
 * because serializers and previews consume those representations. Administrator menu rows
 * expose their plain source fields directly. Reordering delegates to the controller so row
 * identity and menu sort_order stay associated, and tab hiding leaves editor state mounted
 * for the shared save or discard action.
 */
// The parent supplies one controller and its shared draft for this mounted
// panel; template writes mutate that draft before the page save action runs.
const props = defineProps<{
  // imagesDisabled cancels pending reads while the page restores saved settings.
  // Every image editor in this panel remains disabled until that reload finishes.
  imagesDisabled?: boolean
  // controller retains page-scoped refs and actions; its closures target the
  // same form object provided below for this panel's editable controls.
  controller: ReturnType<typeof useGeneralSettings>
  // form is the parent-owned reactive draft; nested writes participate in the
  // bulk dirty tracker and remain visible across tab changes.
  form: GeneralSettingsForm
}>()
// Controller bindings retain their refs and closures so template events use
// the page state that hydration, validation, and shared saving also consume.
const {
  tablePageSizeOptionsInput,
  usageRankingSortOptions,
  previewBalanceUnitName,
  previewBalanceAmount,
  customMenuVisibilityOptions,
  addMenuItem,
  removeMenuItem,
  moveMenuItem,
  addEndpoint,
  removeEndpoint,
  addFooterLink,
  addFooterGroup,
  applyDefaultFooterLinks,
  removeFooterGroup,
  moveFooterGroup,
  homeFeaturedModelsMax,
  homeFeaturedModelOptions,
  removeHomeFeaturedModel,
  moveHomeFeaturedModel,
} = props.controller
// The draft alias keeps nested v-model writes on the parent-owned reactive
// object; the parent maintains its identity throughout this page lifetime.
const { form } = props
// Translation lookups are evaluated by the template so labels follow the
// active interface language while stored setting values retain their API meaning.
const { t } = useI18n()
</script>
