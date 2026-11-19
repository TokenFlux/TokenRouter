import js from '@eslint/js'
import vue from 'eslint-plugin-vue'
import globals from 'globals'
import tseslint from 'typescript-eslint'

// eslint.config.js applies JavaScript, TypeScript, and Vue syntax checks to the
// frontend source and its build scripts. Vue templates use the Vue parser while
// script blocks use the TypeScript parser. Formatting and ownership rules run
// through their dedicated package commands so each diagnostic identifies its check.
export default tseslint.config(
  {
    ignores: [
      'vite.config.js',
      'vite.config.d.ts',
      '**/node_modules/**',
      '**/dist/**',
      '**/coverage/**',
      '**/.cache/**',
      '**/.vite/**',
      '**/.vitest/**',
      '**/*.timestamp-*.mjs',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...vue.configs['flat/essential'],
  {
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: { ...globals.browser, ...globals.node },
      parserOptions: { parser: tseslint.parser, extraFileExtensions: ['.vue'] },
    },
    rules: {
      'no-constant-condition': 'off',
      'no-mixed-spaces-and-tabs': 'off',
      'no-useless-escape': 'off',
      'no-unused-vars': 'off',
      '@typescript-eslint/no-unused-vars': [
        'warn',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' },
      ],
      // Existing API declarations use empty objects, Function, and boxed types; these
      // rules leave those representations available while syntax and usage checks remain active.
      '@typescript-eslint/no-empty-object-type': 'off',
      '@typescript-eslint/no-unsafe-function-type': 'off',
      '@typescript-eslint/no-wrapper-object-types': 'off',
      '@typescript-eslint/ban-ts-comment': 'off',
      '@typescript-eslint/no-explicit-any': 'off',
      'vue/multi-word-component-names': 'off',
      'vue/no-use-v-if-with-v-for': 'off',
    },
  },
  {
    files: ['src/views/**/*.{ts,tsx,vue}', 'src/components/**/*.{ts,tsx,vue}'],
    ignores: ['**/__tests__/**', '**/*.spec.ts', '**/*.test.ts'],
    rules: {
      // Views and components delegate HTTP and streaming transport to src/api/ modules so
      // authentication, locale headers, and 401 token refresh stay centralized.
      'no-restricted-globals': [
        'error',
        {
          name: 'fetch',
          message:
            'Place HTTP and streaming requests in src/api/ instead of calling fetch directly from views or components.',
        },
      ],
    },
  },
)
