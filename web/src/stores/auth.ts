import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export interface User {
  id: string
  username: string
  avatar?: string
  email?: string
}

interface AuthState {
  user: User | null
  accessToken: string | null
  isAuthenticated: boolean
  isLoading: boolean
  error: string | null

  setUser: (user: User | null) => void
  setAccessToken: (token: string | null) => void
  setIsLoading: (loading: boolean) => void
  setError: (error: string | null) => void
  logout: () => void
  reset: () => void
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      accessToken: null,
      isAuthenticated: false,
      isLoading: false,
      error: null,

      setUser: (user) =>
        set({
          user,
          isAuthenticated: !!user,
        }),

      setAccessToken: (token) =>
        set({
          accessToken: token,
        }),

      setIsLoading: (loading) =>
        set({
          isLoading: loading,
        }),

      setError: (error) =>
        set({
          error,
        }),

      logout: () =>
        set({
          user: null,
          accessToken: null,
          isAuthenticated: false,
        }),

      reset: () =>
        set({
          user: null,
          accessToken: null,
          isAuthenticated: false,
          isLoading: false,
          error: null,
        }),
    }),
    {
      name: 'auth-storage',
    }
  )
)
