import type { User } from "@gen/common/v1/types_pb";
import { create } from "zustand";

type AuthState = {
  accessToken: string;
  currentUser: User | null;
  tenantId: string;
  roles: string[];
  scopes: string[];
  bootstrapping: boolean;
  bootstrapError: string;
  setAuthenticated: (
    token: string,
    user: User | null,
    tenantId?: string,
    roles?: string[],
    scopes?: string[],
  ) => void;
  startBootstrap: () => void;
  finishBootstrap: () => void;
  failBootstrap: (error: string) => void;
  clear: () => void;
};

export const useAuthStore = create<AuthState>((set) => ({
  accessToken: "",
  currentUser: null,
  tenantId: "",
  roles: [],
  scopes: [],
  bootstrapping: false,
  bootstrapError: "",
  setAuthenticated: (token, user, tenantId = "", roles = [], scopes = []) => {
    set({
      accessToken: token,
      currentUser: user,
      tenantId,
      roles: [...roles],
      scopes: [...scopes],
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
      tenantId: "",
      roles: [],
      scopes: [],
      bootstrapping: false,
      bootstrapError: "",
    });
  },
}));
