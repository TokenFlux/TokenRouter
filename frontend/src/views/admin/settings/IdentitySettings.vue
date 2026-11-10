<template>
  <SettingsCard
    :title="t('admin.settings.linuxdo.title')"
    :description="t('admin.settings.linuxdo.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="linuxdo-connect-enabled"
        v-model="form.linuxdo_connect_enabled"
        :label="t('admin.settings.linuxdo.enable')"
        :hint="t('admin.settings.linuxdo.enableHint')"
      />
      <Collapse :open="form.linuxdo_connect_enabled" unmount-on-hide>
        <SettingsSubpanel>
          <div class="grid gap-4 md:grid-cols-2">
            <div>
              <label for="linuxdo-client-id" class="input-label">
                {{ t('admin.settings.linuxdo.clientId') }}
              </label>
              <input
                id="linuxdo-client-id"
                v-model="form.linuxdo_connect_client_id"
                type="text"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.linuxdo.clientIdPlaceholder')"
              />
              <p class="input-hint">{{ t('admin.settings.linuxdo.clientIdHint') }}</p>
            </div>
            <div>
              <label for="linuxdo-client-secret" class="input-label">
                {{ t('admin.settings.linuxdo.clientSecret') }}
              </label>
              <input
                id="linuxdo-client-secret"
                v-model="form.linuxdo_connect_client_secret"
                type="password"
                class="input font-mono text-sm"
                :placeholder="
                  form.linuxdo_connect_client_secret_configured
                    ? t('admin.settings.linuxdo.clientSecretConfiguredPlaceholder')
                    : t('admin.settings.linuxdo.clientSecretPlaceholder')
                "
              />
              <p class="input-hint">
                {{
                  form.linuxdo_connect_client_secret_configured
                    ? t('admin.settings.linuxdo.clientSecretConfiguredHint')
                    : t('admin.settings.linuxdo.clientSecretHint')
                }}
              </p>
            </div>
          </div>
          <div>
            <label for="linuxdo-redirect-url" class="input-label">
              {{ t('admin.settings.linuxdo.redirectUrl') }}
            </label>
            <div class="flex gap-2">
              <input
                id="linuxdo-redirect-url"
                v-model="form.linuxdo_connect_redirect_url"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.linuxdo.redirectUrlPlaceholder')"
              />
              <button
                type="button"
                class="btn btn-secondary btn-sm h-9 shrink-0"
                @click="setAndCopyLinuxdoRedirectUrl"
              >
                {{ t('admin.settings.linuxdo.quickSetCopy') }}
              </button>
            </div>
            <p class="input-hint">
              {{ t('admin.settings.linuxdo.redirectUrlHint') }}
              <code v-if="linuxdoRedirectUrlSuggestion" class="select-all break-all font-mono">{{
                linuxdoRedirectUrlSuggestion
              }}</code>
            </p>
          </div>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <!-- GitHub and Google email sign-in settings. -->
  <SettingsCard
    :title="t('admin.settings.emailOAuth.title')"
    :description="t('admin.settings.emailOAuth.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="github-oauth-enabled"
        v-model="form.github_oauth_enabled"
        label="GitHub"
        :hint="t('admin.settings.emailOAuth.githubHint')"
      />
      <Collapse :open="form.github_oauth_enabled" unmount-on-hide>
        <SettingsSubpanel>
          <SettingsNotice>
            {{ t('admin.settings.emailOAuth.githubGuideBeforeLink') }}
            <a
              data-testid="github-oauth-apps-guide-link"
              href="https://github.com/settings/developers"
              target="_blank"
              rel="noopener noreferrer"
              class="font-medium text-link"
              >OAuth Apps</a
            >
            {{ t('admin.settings.emailOAuth.githubGuideAfterLink') }}
          </SettingsNotice>
          <div class="grid gap-4 md:grid-cols-2">
            <div>
              <label for="github-oauth-client-id" class="input-label">Client ID</label>
              <input
                id="github-oauth-client-id"
                v-model="form.github_oauth_client_id"
                type="text"
                class="input font-mono text-sm"
                placeholder="GitHub OAuth Client ID"
              />
            </div>
            <div>
              <label for="github-oauth-client-secret" class="input-label">Client Secret</label>
              <input
                id="github-oauth-client-secret"
                v-model="form.github_oauth_client_secret"
                type="password"
                class="input font-mono text-sm"
                :placeholder="
                  form.github_oauth_client_secret_configured
                    ? t('admin.settings.emailOAuth.secretConfiguredPlaceholder')
                    : 'GitHub OAuth Client Secret'
                "
              />
            </div>
          </div>
          <div>
            <label for="github-oauth-redirect-url" class="input-label">
              {{ t('admin.settings.emailOAuth.backendCallbackUrl') }}
            </label>
            <div class="flex gap-2">
              <input
                id="github-oauth-redirect-url"
                v-model="form.github_oauth_redirect_url"
                type="url"
                class="input font-mono text-sm"
                placeholder="https://your-domain.com/api/v1/auth/oauth/github/callback"
              />
              <button
                type="button"
                class="btn btn-secondary btn-sm h-9 shrink-0"
                @click="setAndCopyEmailOAuthRedirectUrl('github')"
              >
                {{ t('admin.settings.emailOAuth.generateAndCopy') }}
              </button>
            </div>
            <p v-if="githubOAuthRedirectUrlSuggestion" class="input-hint">
              <code class="select-all break-all font-mono">{{
                githubOAuthRedirectUrlSuggestion
              }}</code>
            </p>
          </div>
          <div>
            <label for="github-oauth-frontend-redirect-url" class="input-label">
              {{ t('admin.settings.emailOAuth.frontendCallbackUrl') }}
            </label>
            <input
              id="github-oauth-frontend-redirect-url"
              v-model="form.github_oauth_frontend_redirect_url"
              type="text"
              class="input font-mono text-sm"
              placeholder="/auth/oauth/callback"
            />
          </div>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>

    <SettingsSection>
      <SettingToggleRow
        id="google-oauth-enabled"
        v-model="form.google_oauth_enabled"
        label="Google"
        :hint="t('admin.settings.emailOAuth.googleHint')"
      />
      <Collapse :open="form.google_oauth_enabled" unmount-on-hide>
        <SettingsSubpanel>
          <SettingsNotice>
            {{ t('admin.settings.emailOAuth.googleGuide') }}
          </SettingsNotice>
          <div class="grid gap-4 md:grid-cols-2">
            <div>
              <label for="google-oauth-client-id" class="input-label">Client ID</label>
              <input
                id="google-oauth-client-id"
                v-model="form.google_oauth_client_id"
                type="text"
                class="input font-mono text-sm"
                placeholder="Google OAuth Client ID"
              />
            </div>
            <div>
              <label for="google-oauth-client-secret" class="input-label">Client Secret</label>
              <input
                id="google-oauth-client-secret"
                v-model="form.google_oauth_client_secret"
                type="password"
                class="input font-mono text-sm"
                :placeholder="
                  form.google_oauth_client_secret_configured
                    ? t('admin.settings.emailOAuth.secretConfiguredPlaceholder')
                    : 'Google OAuth Client Secret'
                "
              />
            </div>
          </div>
          <div>
            <label for="google-oauth-redirect-url" class="input-label">
              {{ t('admin.settings.emailOAuth.backendCallbackUrl') }}
            </label>
            <div class="flex gap-2">
              <input
                id="google-oauth-redirect-url"
                v-model="form.google_oauth_redirect_url"
                type="url"
                class="input font-mono text-sm"
                placeholder="https://your-domain.com/api/v1/auth/oauth/google/callback"
              />
              <button
                type="button"
                class="btn btn-secondary btn-sm h-9 shrink-0"
                @click="setAndCopyEmailOAuthRedirectUrl('google')"
              >
                {{ t('admin.settings.emailOAuth.generateAndCopy') }}
              </button>
            </div>
            <p v-if="googleOAuthRedirectUrlSuggestion" class="input-hint">
              <code class="select-all break-all font-mono">{{
                googleOAuthRedirectUrlSuggestion
              }}</code>
            </p>
          </div>
          <div>
            <label for="google-oauth-frontend-redirect-url" class="input-label">
              {{ t('admin.settings.emailOAuth.frontendCallbackUrl') }}
            </label>
            <input
              id="google-oauth-frontend-redirect-url"
              v-model="form.google_oauth_frontend_redirect_url"
              type="text"
              class="input font-mono text-sm"
              placeholder="/auth/oauth/callback"
            />
          </div>
          <SettingToggleRow
            id="google-one-tap-enabled"
            v-model="form.google_one_tap_enabled"
            label="Google One Tap"
            :hint="t('admin.settings.emailOAuth.googleOneTapHint')"
          />
          <!-- One Tap uses the frontend origin shown here for Google Cloud JavaScript origin registration. -->
          <Collapse :open="form.google_one_tap_enabled" unmount-on-hide>
            <div>
              <p class="input-label">Authorized JavaScript origin</p>
              <div class="flex gap-2">
                <code class="input flex items-center select-all break-all font-mono text-sm">
                  {{ googleOneTapOriginSuggestion }}
                </code>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm h-9 shrink-0"
                  :disabled="!googleOneTapOriginSuggestion"
                  @click="copyGoogleOneTapOrigin"
                >
                  <Icon name="copy" size="sm" class="mr-1.5" />
                  {{ t('admin.settings.emailOAuth.copy') }}
                </button>
              </div>
              <p class="input-hint">
                {{ t('admin.settings.emailOAuth.googleOneTapOriginHint') }}
              </p>
            </div>
          </Collapse>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <!-- WeChat sign-in capabilities. -->
  <SettingsCard
    :title="t('admin.settings.wechatConnect.title')"
    :description="t('admin.settings.wechatConnect.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="wechat-connect-enabled"
        v-model="form.wechat_connect_enabled"
        :label="t('admin.settings.wechatConnect.enabledLabel')"
        :hint="t('admin.settings.wechatConnect.enabledHint')"
        testid="wechat-connect-enabled"
      />
      <Collapse :open="form.wechat_connect_enabled" unmount-on-hide>
        <SettingsSubpanel>
          <SettingToggleRow
            id="wechat-connect-open-enabled"
            :model-value="form.wechat_connect_open_enabled"
            :label="t('admin.settings.wechatConnect.pcApp')"
            :hint="t('admin.settings.wechatConnect.pcAppHint')"
            testid="wechat-connect-open-enabled"
            @update:model-value="handleWeChatOpenEnabledChange"
          />
          <Collapse :open="form.wechat_connect_open_enabled" unmount-on-hide>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="wechat-connect-open-app-id" class="input-label">
                  {{ t('admin.settings.wechatConnect.pcAppId') }}
                </label>
                <input
                  id="wechat-connect-open-app-id"
                  v-model="form.wechat_connect_open_app_id"
                  data-testid="wechat-connect-open-app-id"
                  type="text"
                  class="input font-mono text-sm"
                  :placeholder="t('admin.settings.wechatConnect.pcAppIdPlaceholder')"
                />
              </div>
              <div>
                <label for="wechat-connect-open-app-secret" class="input-label">
                  {{ t('admin.settings.wechatConnect.pcAppSecret') }}
                </label>
                <input
                  id="wechat-connect-open-app-secret"
                  v-model="form.wechat_connect_open_app_secret"
                  data-testid="wechat-connect-open-app-secret"
                  type="password"
                  class="input font-mono text-sm"
                  :placeholder="
                    form.wechat_connect_open_app_secret_configured
                      ? t('admin.settings.wechatConnect.appSecretConfiguredPlaceholder')
                      : t('admin.settings.wechatConnect.pcAppSecretPlaceholder')
                  "
                />
              </div>
            </div>
          </Collapse>

          <SettingToggleRow
            id="wechat-connect-mp-enabled"
            :model-value="form.wechat_connect_mp_enabled"
            :label="t('admin.settings.wechatConnect.mp')"
            :hint="t('admin.settings.wechatConnect.mpHint')"
            testid="wechat-connect-mp-enabled"
            @update:model-value="handleWeChatMPEnabledChange"
          />
          <Collapse :open="form.wechat_connect_mp_enabled" unmount-on-hide>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="wechat-connect-mp-app-id" class="input-label">
                  {{ t('admin.settings.wechatConnect.mpAppId') }}
                </label>
                <input
                  id="wechat-connect-mp-app-id"
                  v-model="form.wechat_connect_mp_app_id"
                  data-testid="wechat-connect-mp-app-id"
                  type="text"
                  class="input font-mono text-sm"
                  :placeholder="t('admin.settings.wechatConnect.mpAppId')"
                />
              </div>
              <div>
                <label for="wechat-connect-mp-app-secret" class="input-label">
                  {{ t('admin.settings.wechatConnect.mpAppSecret') }}
                </label>
                <input
                  id="wechat-connect-mp-app-secret"
                  v-model="form.wechat_connect_mp_app_secret"
                  data-testid="wechat-connect-mp-app-secret"
                  type="password"
                  class="input font-mono text-sm"
                  :placeholder="
                    form.wechat_connect_mp_app_secret_configured
                      ? t('admin.settings.wechatConnect.appSecretConfiguredPlaceholder')
                      : t('admin.settings.wechatConnect.mpAppSecret')
                  "
                />
              </div>
            </div>
          </Collapse>

          <SettingToggleRow
            id="wechat-connect-mobile-enabled"
            :model-value="form.wechat_connect_mobile_enabled"
            :label="t('admin.settings.wechatConnect.mobile')"
            :hint="t('admin.settings.wechatConnect.mobileHint')"
            testid="wechat-connect-mobile-enabled"
            @update:model-value="handleWeChatMobileEnabledChange"
          />
          <Collapse :open="form.wechat_connect_mobile_enabled" unmount-on-hide>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="wechat-connect-mobile-app-id" class="input-label">
                  {{ t('admin.settings.wechatConnect.mobileAppId') }}
                </label>
                <input
                  id="wechat-connect-mobile-app-id"
                  v-model="form.wechat_connect_mobile_app_id"
                  data-testid="wechat-connect-mobile-app-id"
                  type="text"
                  class="input font-mono text-sm"
                  :placeholder="t('admin.settings.wechatConnect.mobileAppId')"
                />
              </div>
              <div>
                <label for="wechat-connect-mobile-app-secret" class="input-label">
                  {{ t('admin.settings.wechatConnect.mobileAppSecret') }}
                </label>
                <input
                  id="wechat-connect-mobile-app-secret"
                  v-model="form.wechat_connect_mobile_app_secret"
                  data-testid="wechat-connect-mobile-app-secret"
                  type="password"
                  class="input font-mono text-sm"
                  :placeholder="
                    form.wechat_connect_mobile_app_secret_configured
                      ? t('admin.settings.wechatConnect.appSecretConfiguredPlaceholder')
                      : t('admin.settings.wechatConnect.mobileAppSecret')
                  "
                />
              </div>
            </div>
          </Collapse>

          <!-- Show UnionID guidance when the open capability is combined with either mp or mobile capability. -->
          <Collapse
            :open="
              form.wechat_connect_open_enabled &&
              (form.wechat_connect_mp_enabled || form.wechat_connect_mobile_enabled)
            "
            unmount-on-hide
          >
            <SettingsNotice tone="warning">
              {{ t('admin.settings.wechatConnect.unionIdHint') }}
            </SettingsNotice>
          </Collapse>

          <div>
            <label for="wechat-connect-redirect-url" class="input-label">
              {{ t('admin.settings.wechatConnect.browserRedirectUrl') }}
            </label>
            <div class="flex gap-2">
              <input
                id="wechat-connect-redirect-url"
                v-model="form.wechat_connect_redirect_url"
                data-testid="wechat-connect-redirect-url"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.wechatConnect.redirectUrlPlaceholder')"
              />
              <button
                type="button"
                class="btn btn-secondary btn-sm h-9 shrink-0"
                @click="setAndCopyWeChatRedirectUrl"
              >
                {{ t('admin.settings.wechatConnect.generateAndCopy') }}
              </button>
            </div>
            <p class="input-hint">
              {{ t('admin.settings.wechatConnect.browserRedirectUrlHint') }}
              <code v-if="wechatRedirectUrlSuggestion" class="select-all break-all font-mono">{{
                wechatRedirectUrlSuggestion
              }}</code>
            </p>
          </div>
          <div>
            <label for="wechat-connect-frontend-redirect-url" class="input-label">
              {{ t('admin.settings.wechatConnect.frontendRedirectUrlLabel') }}
            </label>
            <input
              id="wechat-connect-frontend-redirect-url"
              v-model="form.wechat_connect_frontend_redirect_url"
              data-testid="wechat-connect-frontend-redirect-url"
              type="text"
              class="input font-mono text-sm"
              :placeholder="t('admin.settings.wechatConnect.frontendRedirectUrlPlaceholder')"
            />
            <p class="input-hint">
              {{ t('admin.settings.wechatConnect.frontendRedirectUrlHint') }}
            </p>
          </div>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <!-- DingTalk sign-in settings. -->
  <SettingsCard
    :title="t('admin.settings.dingtalk.title')"
    :description="t('admin.settings.dingtalk.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="dingtalk-connect-enabled"
        v-model="form.dingtalk_connect_enabled"
        :label="t('admin.settings.dingtalk.enable')"
        :hint="t('admin.settings.dingtalk.enableHint')"
      />
      <Collapse :open="form.dingtalk_connect_enabled" unmount-on-hide>
        <SettingsSubpanel>
          <div class="grid gap-4 md:grid-cols-2">
            <div>
              <label for="dingtalk-client-id" class="input-label">
                {{ t('admin.settings.dingtalk.clientId') }}
              </label>
              <input
                id="dingtalk-client-id"
                v-model="form.dingtalk_connect_client_id"
                type="text"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.dingtalk.clientIdPlaceholder')"
              />
              <p class="input-hint">{{ t('admin.settings.dingtalk.clientIdHint') }}</p>
            </div>
            <div>
              <label for="dingtalk-client-secret" class="input-label">
                {{ t('admin.settings.dingtalk.clientSecret') }}
              </label>
              <input
                id="dingtalk-client-secret"
                v-model="form.dingtalk_connect_client_secret"
                type="password"
                class="input font-mono text-sm"
                :placeholder="
                  form.dingtalk_connect_client_secret_configured
                    ? t('admin.settings.dingtalk.clientSecretConfiguredPlaceholder')
                    : t('admin.settings.dingtalk.clientSecretPlaceholder')
                "
              />
              <p class="input-hint">
                {{
                  form.dingtalk_connect_client_secret_configured
                    ? t('admin.settings.dingtalk.clientSecretConfiguredHint')
                    : t('admin.settings.dingtalk.clientSecretHint')
                }}
              </p>
            </div>
          </div>
          <div>
            <label for="dingtalk-redirect-url" class="input-label">
              {{ t('admin.settings.dingtalk.redirectUrl') }}
            </label>
            <input
              id="dingtalk-redirect-url"
              v-model="form.dingtalk_connect_redirect_url"
              type="url"
              class="input font-mono text-sm"
              :placeholder="t('admin.settings.dingtalk.redirectUrlPlaceholder')"
            />
            <p class="input-hint">{{ t('admin.settings.dingtalk.redirectUrlHint') }}</p>
          </div>
          <div>
            <p class="input-label">{{ t('admin.settings.dingtalk.corpPolicy.label') }}</p>
            <SettingsSegmented
              v-model="form.dingtalk_connect_corp_restriction_policy"
              :options="dingtalkCorpPolicyOptions"
              :ariaLabel="t('admin.settings.dingtalk.corpPolicy.label')"
              block
            />
            <p class="input-hint">{{ t('admin.settings.dingtalk.corpPolicy.hint') }}</p>
          </div>

          <!-- The internal-only policy exposes registration bypass and identity synchronization; the controller clears those switches when the policy changes. -->
          <template v-if="form.dingtalk_connect_corp_restriction_policy === 'internal_only'">
            <SettingToggleRow
              id="dingtalk-bypass-registration"
              v-model="form.dingtalk_connect_bypass_registration"
              :label="t('admin.settings.dingtalk.bypassRegistration')"
              :hint="t('admin.settings.dingtalk.bypassRegistrationHint')"
            />

            <SettingToggleRow
              id="dingtalk-sync-display-name"
              v-model="form.dingtalk_connect_sync_display_name"
              :label="t('admin.settings.dingtalk.syncDisplayName')"
              :hint="t('admin.settings.dingtalk.syncDisplayNameHint')"
            />
            <Collapse :open="form.dingtalk_connect_sync_display_name" unmount-on-hide>
              <div class="space-y-1">
                <div class="grid gap-4 md:grid-cols-2">
                  <div>
                    <label for="dingtalk-sync-display-name-key" class="input-label">
                      {{ t('admin.settings.dingtalk.syncDisplayNameTarget') }}
                    </label>
                    <input
                      id="dingtalk-sync-display-name-key"
                      v-model="form.dingtalk_connect_sync_display_name_attr_key"
                      type="text"
                      placeholder="dingtalk_name"
                      class="input font-mono text-sm"
                    />
                  </div>
                  <div>
                    <label for="dingtalk-sync-display-name-name" class="input-label">
                      {{ t('admin.settings.dingtalk.syncAttrDisplayName') }}
                    </label>
                    <input
                      id="dingtalk-sync-display-name-name"
                      v-model="form.dingtalk_connect_sync_display_name_attr_name"
                      type="text"
                      placeholder="钉钉姓名"
                      class="input"
                    />
                  </div>
                </div>
                <p class="input-hint">
                  {{ t('admin.settings.dingtalk.syncDisplayNameTargetHint') }}
                </p>
              </div>
            </Collapse>

            <SettingToggleRow
              id="dingtalk-sync-corp-email"
              v-model="form.dingtalk_connect_sync_corp_email"
              :label="t('admin.settings.dingtalk.syncCorpEmail')"
              :hint="t('admin.settings.dingtalk.syncCorpEmailHint')"
            >
              <template #hint>
                <SettingsNotice tone="warning" class="mt-2">
                  {{ t('admin.settings.dingtalk.syncCorpEmailPermissionHint') }}
                </SettingsNotice>
              </template>
            </SettingToggleRow>
            <Collapse :open="form.dingtalk_connect_sync_corp_email" unmount-on-hide>
              <div class="space-y-1">
                <div class="grid gap-4 md:grid-cols-2">
                  <div>
                    <label for="dingtalk-sync-corp-email-key" class="input-label">
                      {{ t('admin.settings.dingtalk.syncCorpEmailTarget') }}
                    </label>
                    <input
                      id="dingtalk-sync-corp-email-key"
                      v-model="form.dingtalk_connect_sync_corp_email_attr_key"
                      type="text"
                      placeholder="dingtalk_email"
                      class="input font-mono text-sm"
                    />
                  </div>
                  <div>
                    <label for="dingtalk-sync-corp-email-name" class="input-label">
                      {{ t('admin.settings.dingtalk.syncAttrDisplayName') }}
                    </label>
                    <input
                      id="dingtalk-sync-corp-email-name"
                      v-model="form.dingtalk_connect_sync_corp_email_attr_name"
                      type="text"
                      placeholder="钉钉企业邮箱"
                      class="input"
                    />
                  </div>
                </div>
                <p class="input-hint">
                  {{ t('admin.settings.dingtalk.syncCorpEmailTargetHint') }}
                </p>
              </div>
            </Collapse>

            <SettingToggleRow
              id="dingtalk-sync-dept"
              v-model="form.dingtalk_connect_sync_dept"
              :label="t('admin.settings.dingtalk.syncDept')"
              :hint="t('admin.settings.dingtalk.syncDeptHint')"
            >
              <template #hint>
                <SettingsNotice tone="warning" class="mt-2">
                  {{ t('admin.settings.dingtalk.syncDeptPermissionHint') }}
                </SettingsNotice>
              </template>
            </SettingToggleRow>
            <Collapse :open="form.dingtalk_connect_sync_dept" unmount-on-hide>
              <div class="space-y-1">
                <div class="grid gap-4 md:grid-cols-2">
                  <div>
                    <label for="dingtalk-sync-dept-key" class="input-label">
                      {{ t('admin.settings.dingtalk.syncDeptTarget') }}
                    </label>
                    <input
                      id="dingtalk-sync-dept-key"
                      v-model="form.dingtalk_connect_sync_dept_attr_key"
                      type="text"
                      placeholder="dingtalk_department"
                      class="input font-mono text-sm"
                    />
                  </div>
                  <div>
                    <label for="dingtalk-sync-dept-name" class="input-label">
                      {{ t('admin.settings.dingtalk.syncAttrDisplayName') }}
                    </label>
                    <input
                      id="dingtalk-sync-dept-name"
                      v-model="form.dingtalk_connect_sync_dept_attr_name"
                      type="text"
                      placeholder="钉钉部门"
                      class="input"
                    />
                  </div>
                </div>
                <p class="input-hint">
                  {{ t('admin.settings.dingtalk.syncDeptTargetHint') }}
                </p>
              </div>
            </Collapse>
          </template>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <!-- OpenID Connect provider settings. -->
  <SettingsCard
    :title="t('admin.settings.oidc.title')"
    :description="t('admin.settings.oidc.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="oidc-connect-enabled"
        v-model="form.oidc_connect_enabled"
        :label="t('admin.settings.oidc.enable')"
        :hint="t('admin.settings.oidc.enableHint')"
      />
      <Collapse :open="form.oidc_connect_enabled" unmount-on-hide>
        <SettingsSubpanel>
          <div class="grid gap-4 md:grid-cols-3">
            <div>
              <LocalizedEditor
                :label="t('admin.settings.oidc.providerName')"
                :placeholder="t('admin.settings.oidc.providerNamePlaceholder')"
                v-model="form.localized_settings.oidc_connect_provider_name"
                :default-source-locale="getLocale()"
              />
            </div>
            <div>
              <label for="oidc-client-id" class="input-label">
                {{ t('admin.settings.oidc.clientId') }}
              </label>
              <input
                id="oidc-client-id"
                v-model="form.oidc_connect_client_id"
                type="text"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.clientIdPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-client-secret" class="input-label">
                {{ t('admin.settings.oidc.clientSecret') }}
              </label>
              <input
                id="oidc-client-secret"
                v-model="form.oidc_connect_client_secret"
                type="password"
                class="input font-mono text-sm"
                :placeholder="
                  form.oidc_connect_client_secret_configured
                    ? t('admin.settings.oidc.clientSecretConfiguredPlaceholder')
                    : t('admin.settings.oidc.clientSecretPlaceholder')
                "
              />
              <p class="input-hint">
                {{
                  form.oidc_connect_client_secret_configured
                    ? t('admin.settings.oidc.clientSecretConfiguredHint')
                    : t('admin.settings.oidc.clientSecretHint')
                }}
              </p>
            </div>
          </div>

          <div class="grid gap-4 md:grid-cols-2">
            <div>
              <label for="oidc-issuer-url" class="input-label">{{
                t('admin.settings.oidc.issuerUrl')
              }}</label>
              <input
                id="oidc-issuer-url"
                v-model="form.oidc_connect_issuer_url"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.issuerUrlPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-discovery-url" class="input-label">{{
                t('admin.settings.oidc.discoveryUrl')
              }}</label>
              <input
                id="oidc-discovery-url"
                v-model="form.oidc_connect_discovery_url"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.discoveryUrlPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-authorize-url" class="input-label">{{
                t('admin.settings.oidc.authorizeUrl')
              }}</label>
              <input
                id="oidc-authorize-url"
                v-model="form.oidc_connect_authorize_url"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.authorizeUrlPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-token-url" class="input-label">{{
                t('admin.settings.oidc.tokenUrl')
              }}</label>
              <input
                id="oidc-token-url"
                v-model="form.oidc_connect_token_url"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.tokenUrlPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-userinfo-url" class="input-label">{{
                t('admin.settings.oidc.userinfoUrl')
              }}</label>
              <input
                id="oidc-userinfo-url"
                v-model="form.oidc_connect_userinfo_url"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.userinfoUrlPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-jwks-url" class="input-label">{{
                t('admin.settings.oidc.jwksUrl')
              }}</label>
              <input
                id="oidc-jwks-url"
                v-model="form.oidc_connect_jwks_url"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.jwksUrlPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-scopes" class="input-label">{{
                t('admin.settings.oidc.scopes')
              }}</label>
              <input
                id="oidc-scopes"
                v-model="form.oidc_connect_scopes"
                type="text"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.scopesPlaceholder')"
              />
              <p class="input-hint">{{ t('admin.settings.oidc.scopesHint') }}</p>
            </div>
            <div>
              <label for="oidc-frontend-redirect-url" class="input-label">
                {{ t('admin.settings.oidc.frontendRedirectUrl') }}
              </label>
              <input
                id="oidc-frontend-redirect-url"
                v-model="form.oidc_connect_frontend_redirect_url"
                type="text"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.frontendRedirectUrlPlaceholder')"
              />
              <p class="input-hint">
                {{ t('admin.settings.oidc.frontendRedirectUrlHint') }}
              </p>
            </div>
          </div>

          <div>
            <label for="oidc-redirect-url" class="input-label">{{
              t('admin.settings.oidc.redirectUrl')
            }}</label>
            <div class="flex gap-2">
              <input
                id="oidc-redirect-url"
                v-model="form.oidc_connect_redirect_url"
                type="url"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.redirectUrlPlaceholder')"
              />
              <button
                type="button"
                class="btn btn-secondary btn-sm h-9 shrink-0"
                @click="setAndCopyOIDCRedirectUrl"
              >
                {{ t('admin.settings.oidc.quickSetCopy') }}
              </button>
            </div>
            <p class="input-hint">
              {{ t('admin.settings.oidc.redirectUrlHint') }}
              <code v-if="oidcRedirectUrlSuggestion" class="select-all break-all font-mono">{{
                oidcRedirectUrlSuggestion
              }}</code>
            </p>
          </div>

          <div class="grid gap-4 md:grid-cols-3">
            <div>
              <label for="oidc-token-auth-method" class="input-label">
                {{ t('admin.settings.oidc.tokenAuthMethod') }}
              </label>
              <Select
                id="oidc-token-auth-method"
                v-model="form.oidc_connect_token_auth_method"
                :options="oidcTokenAuthMethodOptions"
                class="font-mono text-sm"
              />
            </div>
            <div>
              <label for="oidc-clock-skew" class="input-label">
                {{ t('admin.settings.oidc.clockSkewSeconds') }}
              </label>
              <input
                id="oidc-clock-skew"
                v-model.number="form.oidc_connect_clock_skew_seconds"
                type="number"
                min="0"
                max="600"
                class="input"
              />
            </div>
            <div>
              <label for="oidc-allowed-signing-algs" class="input-label">
                {{ t('admin.settings.oidc.allowedSigningAlgs') }}
              </label>
              <input
                id="oidc-allowed-signing-algs"
                v-model="form.oidc_connect_allowed_signing_algs"
                type="text"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.allowedSigningAlgsPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-userinfo-email-path" class="input-label">
                {{ t('admin.settings.oidc.userinfoEmailPath') }}
              </label>
              <input
                id="oidc-userinfo-email-path"
                v-model="form.oidc_connect_userinfo_email_path"
                type="text"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.userinfoEmailPathPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-userinfo-id-path" class="input-label">
                {{ t('admin.settings.oidc.userinfoIdPath') }}
              </label>
              <input
                id="oidc-userinfo-id-path"
                v-model="form.oidc_connect_userinfo_id_path"
                type="text"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.userinfoIdPathPlaceholder')"
              />
            </div>
            <div>
              <label for="oidc-userinfo-username-path" class="input-label">
                {{ t('admin.settings.oidc.userinfoUsernamePath') }}
              </label>
              <input
                id="oidc-userinfo-username-path"
                v-model="form.oidc_connect_userinfo_username_path"
                type="text"
                class="input font-mono text-sm"
                :placeholder="t('admin.settings.oidc.userinfoUsernamePathPlaceholder')"
              />
            </div>
          </div>

          <SettingToggleRow
            id="oidc-use-pkce"
            v-model="form.oidc_connect_use_pkce"
            :label="t('admin.settings.oidc.usePkce')"
            testid="oidc-connect-use-pkce"
          />
          <SettingToggleRow
            id="oidc-validate-id-token"
            v-model="form.oidc_connect_validate_id_token"
            :label="t('admin.settings.oidc.validateIdToken')"
            testid="oidc-connect-validate-id-token"
          />
          <SettingToggleRow
            id="oidc-require-email-verified"
            v-model="form.oidc_connect_require_email_verified"
            :label="t('admin.settings.oidc.requireEmailVerified')"
          />
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>
</template>

<script setup lang="ts">
import Collapse from '@/components/common/Collapse.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import SettingsSubpanel from '@/components/common/settings/SettingsSubpanel.vue'
import LocalizedEditor from '@/components/common/LocalizedEditor.vue'
import { getLocale } from '@/i18n'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import type { useIdentitySettings, IdentitySettingsForm } from './useIdentitySettings'

/**
 * IdentitySettings.vue presents OAuth credentials, callback addresses, WeChat
 * capabilities, and DingTalk identity synchronization controls. Provider subpanels
 * edit the parent-owned settings draft, and callback buttons delegate draft updates
 * and clipboard feedback to the identity controller during the page lifetime.
 */

/**
 * props supplies the page-owned controller and mutable draft used by this panel
 * throughout its mounted lifetime.
 */
const props = defineProps<{
  /** controller retains reactive fields and actions created once by the parent during page setup. */
  controller: ReturnType<typeof useIdentitySettings>
  /** form shares editable settings with the parent; field writes enter its bulk dirty snapshot. */
  form: IdentitySettingsForm
}>()
/**
 * Controller bindings retain Vue refs and action functions so template edits and
 * buttons use the parent instance.
 */
const {
  oidcTokenAuthMethodOptions,
  dingtalkCorpPolicyOptions,
  linuxdoRedirectUrlSuggestion,
  setAndCopyLinuxdoRedirectUrl,
  githubOAuthRedirectUrlSuggestion,
  googleOAuthRedirectUrlSuggestion,
  googleOneTapOriginSuggestion,
  copyGoogleOneTapOrigin,
  setAndCopyEmailOAuthRedirectUrl,
  wechatRedirectUrlSuggestion,
  handleWeChatOpenEnabledChange,
  handleWeChatMPEnabledChange,
  handleWeChatMobileEnabledChange,
  setAndCopyWeChatRedirectUrl,
  oidcRedirectUrlSuggestion,
  setAndCopyOIDCRedirectUrl,
} = props.controller
/**
 * form retains the shared draft object so nested field edits reach the page save
 * target.
 */
const { form } = props
/**
 * t resolves settings labels and hints from the active interface locale during
 * rendering.
 */
const { t } = useI18n()
</script>
