import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      // exhaustive-deps stays a warning because there are legitimate cases
      // (mount-only fetches, intentional dep omission with explanation) that
      // get suppressed locally with eslint-disable-next-line. Promoting it
      // to error would require either tweaking those callsites or scattering
      // suppressions, both of which add noise without catching real bugs.
      'react-hooks/exhaustive-deps': 'warn',
      // The rest are full errors — we want regressions to fail CI now that
      // the existing violations have been resolved.
      'react-hooks/set-state-in-effect': 'error',
      'react-hooks/purity': 'error',
      'react-hooks/refs': 'error',
      'react-hooks/immutability': 'error',
      'react-hooks/incompatible-library': 'error',
      'react-refresh/only-export-components': 'error',
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-unused-expressions': 'error',
    },
  },
])
