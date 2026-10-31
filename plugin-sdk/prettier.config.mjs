// prettier.config.mjs keeps SDK and example source aligned with the repository's
// quotes, indentation, and line width. The standalone configuration avoids loading
// host application tooling into third-party SDK installations while preserving
// literal embedded source and Vue template whitespace.

export default {
  semi: false,
  singleQuote: true,
  tabWidth: 2,
  printWidth: 100,
  trailingComma: 'all',
  endOfLine: 'lf',
  htmlWhitespaceSensitivity: 'strict',
  embeddedLanguageFormatting: 'off',
  vueIndentScriptAndStyle: false,
}
