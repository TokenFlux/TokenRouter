<template>
  <a
    :href="TOKENROUTER_REPO_URL"
    target="_blank"
    rel="noopener noreferrer"
    class="inline-flex h-7 items-center gap-2 rounded-full border border-gray-200 px-3 text-xs text-gray-500 transition hover:border-primary-600 hover:text-primary-600 focus-visible:border-primary-600 focus-visible:text-primary-600 dark:border-dark-600 dark:text-dark-400 dark:hover:border-primary-500 dark:hover:text-primary-500 dark:focus-visible:border-primary-500 dark:focus-visible:text-primary-500"
  >
    <img src="/logo.svg" alt="" class="h-4 w-4 shrink-0 select-none" />
    <i18n-t keypath="home.footer.poweredBy" tag="span" scope="global">
      <template #name>
        <span class="font-semibold">TokenRouter</span>
      </template>
    </i18n-t>
    <GitHubMark class="h-3.5 w-3.5 shrink-0" />
  </a>
</template>

<script setup lang="ts">
import GitHubMark from '@/components/auth/GitHubMark.vue'

// PoweredByBadge.vue renders the homepage footer link that credits TokenRouter
// and opens its GitHub repository in a new tab. The badge always shows the
// bundled TokenRouter mark from public/logo.svg. The configured site logo
// belongs to the operator, while this badge credits the software running the site.
//
// The translated sentence receives the product name through a named slot, so each
// locale can place the name where its grammar requires while the name itself stays
// untranslated and bold. The logo and GitHub mark are decorative; the link text
// already names the destination for assistive technology.

// TOKENROUTER_REPO_URL is the public repository opened when a visitor follows the
// footer credit.
const TOKENROUTER_REPO_URL = 'https://github.com/TokenFlux/TokenRouter'
</script>
