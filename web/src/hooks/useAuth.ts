import { useCallback, useState } from "react";
import { useAuthStore } from "@/stores/auth";
import { authClient } from "@/api/client";
import type {
  LoginRequest,
  LoginResponse,
  RegisterResponse,
} from "@/gen/gateway/v1/api_pb";

interface UseAuthReturn {
  login: (username: string, password: string) => Promise<void>;
  register: (username: string, password: string) => Promise<void>;
  logout: () => void;
  isLoading: boolean;
  error: string | null;
}

export function useAuth(): UseAuthReturn {
  const {
    setUser,
    setAccessToken,
    setIsLoading: setAuthLoading,
    setError: setAuthError,
  } = useAuthStore();
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const login = useCallback(
    async (username: string, password: string) => {
      setIsLoading(true);
      setError(null);
      setAuthLoading(true);
      setAuthError(null);

      try {
        const response = (await authClient.login({
          username,
          password,
        })) as LoginResponse;

        if (response.user) {
          setUser({
            id: response.user.username,
            username: response.user.username,
            avatar: response.user.avatarUrl,
          });
        }
        setAccessToken(response.accessToken);
      } catch (err) {
        const errorMessage = err instanceof Error ? err.message : "登录失败";
        setError(errorMessage);
        setAuthError(errorMessage);
        throw err;
      } finally {
        setIsLoading(false);
        setAuthLoading(false);
      }
    },
    [setUser, setAccessToken, setAuthLoading, setAuthError],
  );

  const register = useCallback(
    async (username: string, password: string) => {
      setIsLoading(true);
      setError(null);
      setAuthLoading(true);
      setAuthError(null);

      try {
        const response = (await authClient.register({
          username,
          password,
        })) as RegisterResponse;

        if (response.user) {
          setUser({
            id: response.user.username,
            username: response.user.username,
            avatar: response.user.avatarUrl,
          });
        }
        setAccessToken(response.accessToken);
      } catch (err) {
        const errorMessage = err instanceof Error ? err.message : "注册失败";
        setError(errorMessage);
        setAuthError(errorMessage);
        throw err;
      } finally {
        setIsLoading(false);
        setAuthLoading(false);
      }
    },
    [setUser, setAccessToken, setAuthLoading, setAuthError],
  );

  const logout = useCallback(() => {
    setUser(null);
    setAccessToken(null);
    setError(null);
  }, [setUser, setAccessToken]);

  return {
    login,
    register,
    logout,
    isLoading,
    error,
  };
}
