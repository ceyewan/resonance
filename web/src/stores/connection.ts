import { create } from "zustand";

export type ConnectionStatus = "idle" | "connecting" | "open" | "offline";

type ConnectionState = {
  status: ConnectionStatus;
  lastConnectingAtMs: number;
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
  lastConnectingAtMs: 0,
  lastConnectedAtMs: 0,
  lastDisconnectedAtMs: 0,
  lastError: "",
  setConnecting: () => {
    set({
      status: "connecting",
      lastConnectingAtMs: Date.now(),
    });
  },
  setOpen: () => {
    set({
      status: "open",
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
      lastConnectingAtMs: 0,
      lastConnectedAtMs: 0,
      lastDisconnectedAtMs: 0,
      lastError: "",
    });
  },
}));
