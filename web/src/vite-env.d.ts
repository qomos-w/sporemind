/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_SPOREMIND_VERSION?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
