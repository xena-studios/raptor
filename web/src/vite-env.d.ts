/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_URL?: string;
  readonly VITE_TURNSTILE_URL?: string;
  readonly VITE_TURNSTILE_SITEKEY?: string;
}
