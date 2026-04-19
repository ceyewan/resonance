import type { User } from "@gen/common/v1/types_pb";
import { create } from "zustand";

type AuthState = {
  accessToken: string;
  currentUser: User | null;
  bootstrapping: boolean;
  bootstrapError: string;
  setAuthenticated: (token: string, user: User | null) => void;
  startBootstrap: () => void;
  finishBootstrap: () => void;
  failBootstrap: (error: string) => void;
  clear: () => void;
};

export const useAuthStore = create<AuthState>((set) => ({
  accessToken: "",
  currentUser: null,
  bootstrapping: false,
  bootstrapError: "",
  setAuthenticated: (token, user) => {
    set({
      accessToken: token,
      currentUser: user,
      bootstrapError: "",
    });
  },
  startBootstrap: () => {
    set({
      bootstrapping: true,
      bootstrapError: "",
    });
  },
  finishBootstrap: () => {
    set({
      bootstrapping: false,
      bootstrapError: "",
    });
  },
  failBootstrap: (error) => {
    set({
      bootstrapping: false,
      bootstrapError: error,
    });
  },
  clear: () => {
    set({
      accessToken: "",
      currentUser: null,
      bootstrapping: false,
      bootstrapError: "",
    });
  },
}));
