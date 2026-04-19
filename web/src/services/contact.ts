import type { ContactInfo } from "@gen/common/v1/view_pb";
import { SessionType } from "@gen/common/v1/session_pb";

import { syncSessionList } from "./session";

export async function getContactList(): Promise<ContactInfo[]> {
  const { sessionClient } = await import("../api/clients");
  const response = await sessionClient.getContactList({});
  return response.contacts;
}

export async function searchUsers(query: string): Promise<ContactInfo[]> {
  const normalizedQuery = query.trim();
  if (normalizedQuery === "") {
    return [];
  }

  const { sessionClient } = await import("../api/clients");
  const response = await sessionClient.searchUser({ query: normalizedQuery });
  return response.users;
}

export async function createDirectSession(username: string): Promise<string> {
  const normalizedUsername = username.trim();
  if (normalizedUsername === "") {
    throw new Error("Username is required");
  }

  const { sessionClient } = await import("../api/clients");
  const response = await sessionClient.createSession({
    members: [normalizedUsername],
    name: "",
    type: SessionType.DIRECT,
  });
  await syncSessionList();
  return response.sessionId;
}

export async function createGroupSession(
  name: string,
  members: string[],
): Promise<string> {
  const normalizedMembers = members.map((member) => member.trim()).filter(Boolean);
  const normalizedName = name.trim();
  if (normalizedName === "") {
    throw new Error("Group name is required");
  }
  if (normalizedMembers.length === 0) {
    throw new Error("At least one member is required");
  }

  const { sessionClient } = await import("../api/clients");
  const response = await sessionClient.createSession({
    members: normalizedMembers,
    name: normalizedName,
    type: SessionType.GROUP,
  });
  await syncSessionList();
  return response.sessionId;
}
