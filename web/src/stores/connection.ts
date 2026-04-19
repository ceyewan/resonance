import { create } from "zustand";

export type ConnectionStatus = "idle" | "connecting" | "open" | "offline";

type ConnectionState = {
  status: ConnectionStatus;
  reconnectAttempts: number;
  lastConnectedAtMs: number;
  lastDisconnectedAtMs: number;
  lastError: string;
  setConnecting: () => void;
  setOpen: () => void;
  setOffline: (error: string) => void;
  reset: () => void;
};

export const useConnectionStore = create<ConnectionState>((set) => ({
  status: "idle",
  reconnectAttempts: 0,
  lastConnectedAtMs: 0,
  lastDisconnectedAtMs: 0,
  lastError: "",
  setConnecting: () => {
    set((state) => ({
      status: "connecting",
      reconnectAttempts: state.reconnectAttempts + 1,
    }));
  },
  setOpen: () => {
    set({
      status: "open",
      reconnectAttempts: 0,
      lastConnectedAtMs: Date.now(),
      lastError: "",
    });
  },
  setOffline: (error: string) => {
    set({
      status: "offline",
      lastDisconnectedAtMs: Date.now(),
      lastError: error,
    });
  },
  reset: () => {
    set({
      status: "idle",
      reconnectAttempts: 0,
      lastConnectedAtMs: 0,
      lastDisconnectedAtMs: 0,
      lastError: "",
    });
  },
}));
