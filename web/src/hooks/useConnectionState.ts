import { useConnectionStore } from "../stores/connection";

export function useConnectionState() {
  return useConnectionStore((state) => ({
    status: state.status,
    lastError: state.lastError,
    inboxSyncing: state.inboxSyncing,
    lastInboxSyncAtMs: state.lastInboxSyncAtMs,
    lastInboxSyncError: state.lastInboxSyncError,
  }));
}
