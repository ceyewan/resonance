import { useAuthStore } from "../stores/auth";

export function useAuthState() {
  return useAuthStore((state) => ({
    accessToken: state.accessToken,
    currentUser: state.currentUser,
    bootstrapping: state.bootstrapping,
    bootstrapError: state.bootstrapError,
    authenticated: state.accessToken.trim() !== "",
  }));
}
