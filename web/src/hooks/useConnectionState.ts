import { useShallow } from "zustand/react/shallow";

import { useConnectionStore } from "../stores/connection";

export function useConnectionState() {
  return useConnectionStore(
    useShallow((state) => ({
      status: state.status,
      lastError: state.lastError,
      inboxSyncing: state.inboxSyncing,
      lastInboxSyncAtMs: state.lastInboxSyncAtMs,
      lastInboxSyncError: state.lastInboxSyncError,
    })),
  );
}
