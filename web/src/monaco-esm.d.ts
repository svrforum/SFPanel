// The slim Monaco build imports deep monaco-editor ESM subpaths
// (esm/vs/editor/editor.api and esm/vs/{language,basic-languages}/*.contribution)
// which ship no bundled .d.ts. Declare them so tsc resolves these side-effect /
// value imports. The typed `monaco` namespace still comes from `import type
// * as Monaco from 'monaco-editor'` in lib/monaco.ts.
declare module 'monaco-editor/esm/*'
