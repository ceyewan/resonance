import { Code, ConnectError } from "@connectrpc/connect";
import type { User } from "@gen/common/v1/types_pb";

import { authClient } from "../api/clients";
import {
  clearAccessToken,
  getAccessToken,
  setAccessToken,
} from "../api/transport";
import { appRuntime } from "../app/runtime";
import { clearAllData } from "../db/repo";
import { useAuthStore } from "../stores/auth";
import { persistCurrentUsername } from "./session";

const CURRENT_USER_KEY = "resonance_current_user";

function storeCurrentUser(user: User | null): void {
  if (user === null) {
    localStorage.removeItem(CURRENT_USER_KEY);
    return;
  }
  localStorage.setItem(CURRENT_USER_KEY, JSON.stringify({
    username: user.username,
    nickname: user.nickname,
    avatarUrl: user.avatarUrl,
  }));
}

function readStoredUser(): User | null {
  const raw = localStorage.getItem(CURRENT_USER_KEY);
  if (raw === null || raw.trim() === "") {
    return null;
  }

  try {
    const parsed = JSON.parse(raw) as {
      username?: string;
      nickname?: string;
      avatarUrl?: string;
    };
    if (parsed.username === undefined || parsed.username.trim() === "") {
      return null;
    }
    return {
      username: parsed.username,
      nickname: parsed.nickname ?? "",
      avatarUrl: parsed.avatarUrl ?? "",
      $typeName: "resonance.common.v1.User",
    } as User;
  } catch {
    localStorage.removeItem(CURRENT_USER_KEY);
    return null;
  }
}

function isUnauthenticatedError(cause: unknown): boolean {
  const error = ConnectError.from(cause);
  return error.code === Code.Unauthenticated;
}

async function persistAuthenticatedState(token: string, user: User | null): Promise<void> {
  setAccessToken(token);
  storeCurrentUser(user);
  if (user !== null && user.username.trim() !== "") {
    await persistCurrentUsername(user.username);
  }
  useAuthStore.getState().setAuthenticated(token, user);
}

export async function login(username: string, password: string): Promise<void> {
  const response = await authClient.login({
    username: username.trim(),
    password,
  });
  await persistAuthenticatedState(response.accessToken, response.user ?? null);
  await appRuntime.start(response.accessToken);
}

export async function restoreAuthSession(): Promise<void> {
  const token = getAccessToken();
  const authStore = useAuthStore.getState();

  if (token === null || token.trim() === "") {
    authStore.clear();
    appRuntime.stop();
    return;
  }

  authStore.startBootstrap();
  const user = readStoredUser();
  authStore.setAuthenticated(token, user);
  if (user !== null && user.username.trim() !== "") {
    await persistCurrentUsername(user.username);
  }

  try {
    await appRuntime.start(token);
    authStore.finishBootstrap();
  } catch (cause: unknown) {
    if (isUnauthenticatedError(cause)) {
      await logout();
      return;
    }
    authStore.failBootstrap(cause instanceof Error ? cause.message : "Bootstrap failed");
    throw cause;
  }
}

export async function logout(): Promise<void> {
  try {
    if ((getAccessToken() ?? "").trim() !== "") {
      await authClient.logout({});
    }
  } catch {
    // 登出以本地清理为准，服务端失败不阻塞退出。
  }

  appRuntime.stop();
  clearAccessToken();
  storeCurrentUser(null);
  useAuthStore.getState().clear();
  await clearAllData();
}
