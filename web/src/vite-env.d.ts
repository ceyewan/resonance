/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string;
  readonly VITE_WS_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

interface Window {
  __RESONANCE_RUNTIME_CONFIG__?: {
    apiBaseUrl?: string;
    wsBaseUrl?: string;
  };
}
