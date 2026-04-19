import { useCallback, useEffect, useState } from "react";
import type { ContactInfo } from "@gen/common/v1/view_pb";

import {
  createDirectSession,
  createGroupSession,
  getContactList,
  searchUsers,
} from "../services/contact";

type ContactDirectoryState = {
  contacts: ContactInfo[];
  searchResults: ContactInfo[];
  loading: boolean;
  searching: boolean;
  error: string;
  refresh: () => Promise<void>;
  search: (query: string) => Promise<void>;
  startDirectSession: (username: string) => Promise<string>;
  startGroupSession: (name: string, members: string[]) => Promise<string>;
};

export function useContactDirectory(): ContactDirectoryState {
  const [contacts, setContacts] = useState<ContactInfo[]>([]);
  const [searchResults, setSearchResults] = useState<ContactInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [searching, setSearching] = useState(false);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setContacts(await getContactList());
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Failed to load contacts");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const search = useCallback(async (query: string) => {
    const normalizedQuery = query.trim();
    if (normalizedQuery === "") {
      setSearchResults([]);
      return;
    }

    setSearching(true);
    setError("");
    try {
      setSearchResults(await searchUsers(normalizedQuery));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Failed to search users");
    } finally {
      setSearching(false);
    }
  }, []);

  const startDirectSession = useCallback(async (username: string) => {
    const sessionId = await createDirectSession(username);
    await refresh();
    return sessionId;
  }, [refresh]);

  const startGroupSession = useCallback(async (name: string, members: string[]) => {
    const sessionId = await createGroupSession(name, members);
    await refresh();
    return sessionId;
  }, [refresh]);

  return {
    contacts,
    searchResults,
    loading,
    searching,
    error,
    refresh,
    search,
    startDirectSession,
    startGroupSession,
  };
}
