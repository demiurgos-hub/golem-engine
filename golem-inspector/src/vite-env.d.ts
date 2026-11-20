/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Overrides the hub WebSocket URL during development. */
  readonly VITE_WS_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
