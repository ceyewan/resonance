function normalize(value?: string): string {
  if (!value) {
    return "";
  }
  return value.trim();
}

function getRuntimeConfig() {
  if (typeof window === "undefined") {
    return {};
  }
  return window.__RESONANCE_RUNTIME_CONFIG__ ?? {};
}

const runtime = getRuntimeConfig();

export const runtimeApiBaseUrl = normalize(runtime.apiBaseUrl);
export const runtimeWsBaseUrl = normalize(runtime.wsBaseUrl);

function isLocalDockerWebHost(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  const host = window.location.hostname;
  const isLocal = host === "localhost" || host === "127.0.0.1";
  return isLocal && window.location.port === "4173";
}

export function defaultApiBaseUrl(): string {
  if (typeof window === "undefined") {
    return "";
  }
  if (import.meta.env.DEV || isLocalDockerWebHost()) {
    return `http://${window.location.hostname}:8080`;
  }
  return window.location.origin;
}

export function defaultWsBaseUrl(): string {
  if (typeof window === "undefined") {
    return "";
  }
  const wsProtocol = window.location.protocol === "https:" ? "wss" : "ws";
  if (import.meta.env.DEV || isLocalDockerWebHost()) {
    return `${wsProtocol}://${window.location.hostname}:8080/ws`;
  }
  return `${wsProtocol}://${window.location.host}/ws`;
}
