import { createConnectTransport } from "@connectrpc/connect-web";
import { createPromiseClient } from "@connectrpc/connect";
import { AuthService, SessionService } from "@/gen/gateway/v1/api_connect";
import { useAuthStore } from "@/stores/auth";
import {
  CreateSessionRequest,
  SearchUserRequest,
  GetContactListRequest
} from "@/gen/gateway/v1/api_pb";

const baseUrl = import.meta.env.VITE_API_BASE_URL || `http://${window.location.hostname}:8080`;

// 创建带 token 的 transport
export const transport = createConnectTransport({
  baseUrl,
  // 添加拦截器，在每个请求中携带 token
  interceptors: [
    (next) => async (req) => {
      const token = useAuthStore.getState().accessToken;
      if (token) {
        req.header.set("Authorization", token);
      }
      return await next(req);
    },
  ],
});

export const authClient = createPromiseClient(AuthService, transport);

// SessionService 需要 token，使用带认证的 transport
export const sessionClient = createPromiseClient(SessionService, transport);

// ============== 封装 API 方法 ==============

/**
 * 创建会话（单聊或群聊）
 */
export async function createSession(params: {
  members: string[];
  name?: string;
  type: 1 | 2; // 1-单聊, 2-群聊
}): Promise<string> {
  const req = new CreateSessionRequest({
    members: params.members,
    name: params.name ?? "",
    type: params.type,
  });
  const resp = await sessionClient.createSession(req);
  return resp.sessionId;
}

/**
 * 搜索用户
 */
export async function searchUsers(query: string): Promise<Array<{
  username: string;
  nickname: string;
  avatarUrl: string;
}>> {
  const req = new SearchUserRequest({ query });
  const resp = await sessionClient.searchUser(req);
  return resp.users.map((u) => ({
    username: u.username,
    nickname: u.nickname,
    avatarUrl: u.avatarUrl,
  }));
}

/**
 * 获取联系人列表
 */
export async function getContactList(): Promise<Array<{
  username: string;
  nickname: string;
  avatarUrl: string;
}>> {
  const req = new GetContactListRequest();
  const resp = await sessionClient.getContactList(req);
  return resp.contacts.map((c) => ({
    username: c.username,
    nickname: c.nickname,
    avatarUrl: c.avatarUrl,
  }));
}
