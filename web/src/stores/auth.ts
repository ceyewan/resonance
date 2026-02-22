import { create } from "zustand";
import { persist } from "zustand/middleware";

/**
 * 用户信息
 * 对应 protobuf resonance.common.v1.User
 */
export interface User {
  username: string;
  nickname?: string;
  avatarUrl?: string;
}

interface AuthState {
  // 状态
  user: User | null;
  accessToken: string | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  error: string | null;

  // Actions
  setUser: (user: User | null) => void;
  setAccessToken: (token: string | null) => void;
  setIsLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  clearError: () => void;

  // 认证操作
  setAuth: (user: User, token: string) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      // 初始状态
      user: null,
      accessToken: null,
      isAuthenticated: false,
      isLoading: false,
      error: null,

      // 设置用户信息
      setUser: (user) =>
        set({
          user,
          isAuthenticated: !!user,
        }),

      // 设置访问令牌
      setAccessToken: (token) =>
        set({
          accessToken: token,
        }),

      // 设置加载状态
      setIsLoading: (isLoading) =>
        set({
          isLoading,
        }),

      // 设置错误信息
      setError: (error) =>
        set({
          error,
        }),

      // 清除错误信息
      clearError: () =>
        set({
          error: null,
        }),

      // 同时设置用户和令牌（登录/注册成功后调用）
      setAuth: (user, token) =>
        set({
          user,
          accessToken: token,
          isAuthenticated: true,
          error: null,
        }),

      // 登出：清除所有状态（包括临时状态如 isLoading）
      logout: () =>
        set({
          user: null,
          accessToken: null,
          isAuthenticated: false,
          isLoading: false,
          error: null,
        }),
    }),
    {
      name: "resonance-auth-storage",
      // 只持久化必要的字段
      partialize: (state) => ({
        user: state.user,
        accessToken: state.accessToken,
        isAuthenticated: state.isAuthenticated,
      }),
    },
  ),
);
