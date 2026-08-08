import { useAuthStore } from "../stores/auth";

export function useAuthState() {
  const state = useAuthStore();
  return {
    accessToken: state.accessToken,
    currentUser: state.currentUser,
    tenantId: state.tenantId,
    roles: state.roles,
    scopes: state.scopes,
    bootstrapping: state.bootstrapping,
    bootstrapError: state.bootstrapError,
    authenticated: state.accessToken.trim() !== "",
  };
}
