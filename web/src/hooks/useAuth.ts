import { useCallback, useState } from "react";
import { useAuthStore } from "@/stores/auth";
import { authClient } from "@/api/client";
import { ERROR_MESSAGES } from "@/constants";

interface UseAuthReturn {
  login: (username: string, password: string) => Promise<void>;
  register: (username: string, password: string, nickname?: string) => Promise<void>;
  logout: () => Promise<void>;
  isLoading: boolean;
  error: string | null;
  clearError: () => void;
}

/**
 * 认证 Hook
 * 处理登录、注册、登出操作
 */
export function useAuth(): UseAuthReturn {
  const { setAuth, logout: clearAuth, setError, clearError } = useAuthStore();

  const [isLoading, setIsLoading] = useState(false);
  const [error, setErrorState] = useState<string | null>(null);

  const clearLocalError = useCallback(() => {
    setErrorState(null);
    clearError();
  }, [clearError]);

  const login = useCallback(
    async (username: string, password: string) => {
      if (!username.trim() || !password.trim()) {
        const errorMsg = ERROR_MESSAGES.INVALID_INPUT;
        setErrorState(errorMsg);
        setError(errorMsg);
        return;
      }

      setIsLoading(true);
      setErrorState(null);
      setError(null);

      try {
        const response = await authClient.login({
          username: username.trim(),
          password,
        });

        const user = response.user
          ? {
              username: response.user.username,
              nickname: response.user.nickname || undefined,
              avatarUrl: response.user.avatarUrl || undefined,
            }
          : null;

        const token = response.accessToken;

        if (!user || !token) {
          throw new Error(ERROR_MESSAGES.AUTH_FAILED);
        }

        setAuth(user, token);
      } catch (err) {
        const errorMsg = err instanceof Error ? err.message : ERROR_MESSAGES.AUTH_FAILED;
        setErrorState(errorMsg);
        setError(errorMsg);
        throw err;
      } finally {
        setIsLoading(false);
      }
    },
    [setAuth, setError],
  );

  const register = useCallback(
    async (username: string, password: string, nickname?: string) => {
      if (!username.trim() || !password.trim()) {
        const errorMsg = ERROR_MESSAGES.INVALID_INPUT;
        setErrorState(errorMsg);
        setError(errorMsg);
        return;
      }

      setIsLoading(true);
      setErrorState(null);
      setError(null);

      try {
        const response = await authClient.register({
          username: username.trim(),
          password,
          nickname: nickname?.trim() || username.trim(),
        });

        const user = response.user
          ? {
              username: response.user.username,
              nickname: response.user.nickname || undefined,
              avatarUrl: response.user.avatarUrl || undefined,
            }
          : null;

        const token = response.accessToken;

        if (!user || !token) {
          throw new Error(ERROR_MESSAGES.REGISTER_FAILED);
        }

        setAuth(user, token);
      } catch (err) {
        const errorMsg = err instanceof Error ? err.message : ERROR_MESSAGES.REGISTER_FAILED;
        setErrorState(errorMsg);
        setError(errorMsg);
        throw err;
      } finally {
        setIsLoading(false);
      }
    },
    [setAuth, setError],
  );

  const logout = useCallback(async () => {
    setIsLoading(true);

    try {
      // 调用登出 API（通知服务器使 token 失效）
      // 即使 API 调用失败，本地状态也会清除
      await authClient.logout({});
    } catch (err) {
      console.error("[useAuth] Logout API call failed:", err);
    } finally {
      // 无论 API 调用成功与否，都清除本地状态
      clearAuth();
      setIsLoading(false);
    }
  }, [clearAuth]);

  return {
    login,
    register,
    logout,
    isLoading,
    error,
    clearError: clearLocalError,
  };
}
