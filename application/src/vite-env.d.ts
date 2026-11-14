/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 'true' to serve /api/v1 from the in-browser MSW mock IEL. */
  readonly VITE_API_MOCK?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
