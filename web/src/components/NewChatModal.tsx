import { useState, useCallback, useEffect, useRef } from "react";
import { searchUsers, getContactList, createSession } from "@/api/client";
import { cn } from "@/lib/cn";

export interface ContactInfo {
  username: string;
  nickname: string;
  avatarUrl: string;
}

interface NewChatModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSessionCreated: (sessionId: string) => void;
  currentUsername?: string;
}

type ChatMode = "single" | "group";

/**
 * 新建聊天弹窗
 * Liquid Glass T2 级实现 - Modal 组件使用高强度玻璃效果
 */
export function NewChatModal({
  isOpen,
  onClose,
  onSessionCreated,
  currentUsername,
}: NewChatModalProps) {
  const [mode, setMode] = useState<ChatMode>("single");
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<ContactInfo[]>([]);
  const [contacts, setContacts] = useState<ContactInfo[]>([]);
  const [selectedUsers, setSelectedUsers] = useState<string[]>([]);
  const [groupName, setGroupName] = useState("");
  const [isSearching, setIsSearching] = useState(false);
  const [isLoadingContacts, setIsLoadingContacts] = useState(false);
  const [isCreating, setIsCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSearch = useCallback(async () => {
    const query = searchQuery.trim();
    if (!query) {
      setSearchResults([]);
      return;
    }
    setIsSearching(true);
    setError(null);
    try {
      const result = await searchUsers(query);
      setSearchResults(result.filter((u) => u.username !== currentUsername));
    } catch (err) {
      console.error("[NewChatModal] Failed to search users:", err);
      setError("搜索用户失败");
    } finally {
      setIsSearching(false);
    }
  }, [searchQuery, currentUsername]);

  useEffect(() => {
    if (mode === "single") {
      const timer = setTimeout(() => handleSearch(), 300);
      return () => clearTimeout(timer);
    }
  }, [mode, searchQuery, handleSearch]);

  const loadContacts = useCallback(async () => {
    setIsLoadingContacts(true);
    setError(null);
    try {
      const result = await getContactList();
      setContacts(result);
    } catch (err) {
      console.error("[NewChatModal] Failed to load contacts:", err);
      setError("加载联系人失败");
    } finally {
      setIsLoadingContacts(false);
    }
  }, []);

  useEffect(() => {
    if (mode === "group" && contacts.length === 0) {
      loadContacts();
    }
  }, [mode, contacts.length, loadContacts]);

  useEffect(() => {
    setSelectedUsers([]);
    setGroupName("");
    setSearchQuery("");
    setSearchResults([]);
    setError(null);
  }, [mode]);

  const handleClose = useCallback(() => {
    setSelectedUsers([]);
    setGroupName("");
    setSearchQuery("");
    setSearchResults([]);
    setError(null);
    setMode("single");
    onClose();
  }, [onClose]);

  const toggleUser = useCallback(
    (username: string) => {
      if (mode === "single") {
        quickStartChat(username);
        return;
      }
      setSelectedUsers((prev) =>
        prev.includes(username)
          ? prev.filter((u) => u !== username)
          : [...prev, username]
      );
      setError(null);
    },
    [mode]
  );

  const quickStartChat = useCallback(
    async (username: string) => {
      setIsCreating(true);
      setError(null);
      try {
        const sessionId = await createSession({
          members: [username],
          type: 1,
        });
        onSessionCreated(sessionId);
        handleClose();
      } catch (err) {
        console.error("[NewChatModal] Failed to create session:", err);
        setError("创建会话失败，请重试");
      } finally {
        setIsCreating(false);
      }
    },
    [onSessionCreated, handleClose]
  );

  const handleCreateGroup = useCallback(async () => {
    if (selectedUsers.length === 0) {
      setError("请选择至少一个成员");
      return;
    }
    if (selectedUsers.length < 2) {
      setError("群聊至少需要 2 个成员");
      return;
    }
    if (!groupName.trim()) {
      setError("请输入群名");
      return;
    }

    setIsCreating(true);
    setError(null);
    try {
      const sessionId = await createSession({
        members: selectedUsers,
        name: groupName.trim(),
        type: 2,
      });
      onSessionCreated(sessionId);
      handleClose();
    } catch (err) {
      console.error("[NewChatModal] Failed to create group:", err);
      setError("创建群聊失败，请重试");
    } finally {
      setIsCreating(false);
    }
  }, [selectedUsers, groupName, onSessionCreated, handleClose]);

  const dialogRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!isOpen) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !isCreating) handleClose();
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, isCreating, handleClose]);

  if (!isOpen) return null;

  const isGroupMode = mode === "group";
  const displayList = isGroupMode ? contacts : searchResults;
  const isLoading = isGroupMode ? isLoadingContacts : isSearching;

  return (
    <div
      className="lg-modal-overlay fixed inset-0 z-50 flex items-center justify-center p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="new-chat-modal-title"
      onClick={() => !isCreating && handleClose()}
    >
      <div
        ref={dialogRef}
        className="lg-modal-content lg-animate-in w-full max-w-md rounded-3xl shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* 头部 */}
        <div className="flex items-center justify-between border-b border-white/40 p-4 dark:border-white/10">
          <h2 id="new-chat-modal-title" className="text-lg font-semibold text-slate-900 dark:text-white">
            新建聊天
          </h2>
          <button
            onClick={handleClose}
            className="rounded-full p-1.5 text-slate-500 transition-all hover:bg-white/50 hover:text-slate-700 dark:text-slate-300 dark:hover:bg-white/10 dark:hover:text-slate-100"
            disabled={isCreating}
          >
            <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* 模式切换 */}
        <div className="flex border-b border-white/40 dark:border-white/10">
          {(["single", "group"] as const).map((m) => (
            <button
              key={m}
              onClick={() => setMode(m)}
              disabled={isCreating}
              className={cn(
                "flex-1 px-4 py-3 text-sm font-medium transition-all",
                mode === m
                  ? "border-b-2 border-sky-500 text-sky-600 dark:text-sky-400"
                  : "text-slate-500 hover:bg-white/30 dark:text-slate-400 dark:hover:bg-white/5",
                "disabled:opacity-50",
              )}
            >
              {m === "single" ? "单聊" : "群聊"}
            </button>
          ))}
        </div>

        {/* 搜索框 */}
        <div className="p-4">
          <div className="relative">
            <svg className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder={isGroupMode ? "搜索联系人" : "搜索用户名或昵称"}
              disabled={isCreating}
              className={cn("lg-input w-full py-2 pl-10 pr-4 text-sm", "disabled:opacity-50")}
              autoFocus
            />
          </div>
        </div>

        {/* 用户列表 */}
        <div className="max-h-72 overflow-y-auto px-2">
          {isLoading ? (
            <div className="flex items-center justify-center p-8">
              <svg className="h-5 w-5 animate-spin text-slate-400" fill="none" viewBox="0 0 24 24">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
              </svg>
            </div>
          ) : displayList.length === 0 ? (
            <div className="flex flex-col items-center p-8 text-center">
              <svg className="mb-3 h-12 w-12 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z" />
              </svg>
              <p className="text-sm text-slate-500 dark:text-slate-400">
                {isGroupMode
                  ? contacts.length === 0
                    ? "暂无联系人"
                    : "未找到匹配的联系人"
                  : searchQuery
                    ? "未找到用户"
                    : "输入搜索关键词"}
              </p>
            </div>
          ) : (
            <div className="space-y-1 p-2">
              {displayList.map((user) => {
                const isSelected = selectedUsers.includes(user.username);
                return (
                  <div
                    key={user.username}
                    onClick={() => !isCreating && toggleUser(user.username)}
                    className={cn(
                      "flex cursor-pointer items-center gap-3 rounded-xl border border-transparent p-3 transition-all",
                      "hover:border-white/40 dark:hover:border-white/10",
                      isCreating && "cursor-not-allowed opacity-50",
                    )}
                  >
                    {/* 复选框（群聊模式） */}
                    {isGroupMode && (
                      <div
                        className={cn(
                          "flex h-5 w-5 shrink-0 items-center justify-center rounded border transition-colors",
                          isSelected ? "border-sky-500 bg-sky-500" : "border-slate-300 dark:border-slate-500",
                        )}
                      >
                        {isSelected && (
                          <svg className="h-3 w-3 text-white" fill="currentColor" viewBox="0 0 20 20">
                            <path fillRule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clipRule="evenodd" />
                          </svg>
                        )}
                      </div>
                    )}

                    {/* 头像 */}
                    <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-sky-400 to-sky-600 text-sm font-semibold text-white shadow-lg">
                      {(user.nickname || user.username).charAt(0).toUpperCase()}
                    </div>

                    {/* 用户信息 */}
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium text-slate-900 dark:text-white">
                        {user.nickname || user.username}
                      </p>
                      <p className="truncate text-xs text-slate-500 dark:text-slate-400">@{user.username}</p>
                    </div>

                    {/* 单聊模式箭头 */}
                    {!isGroupMode && (
                      <svg className="h-5 w-5 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
                      </svg>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* 群名输入 */}
        {isGroupMode && (
          <div className="border-t border-white/40 p-4 dark:border-white/10">
            <input
              type="text"
              value={groupName}
              onChange={(e) => setGroupName(e.target.value)}
              placeholder="输入群名"
              disabled={isCreating}
              className={cn("lg-input w-full px-4 py-2 text-sm", "disabled:opacity-50")}
            />
          </div>
        )}

        {/* 错误提示 */}
        {error && (
          <div className="px-4 pb-2">
            <p className="text-sm text-red-500 dark:text-red-400">{error}</p>
          </div>
        )}

        {/* 底部操作 */}
        <div className="flex border-t border-white/40 p-4 dark:border-white/10">
          <button
            onClick={handleClose}
            disabled={isCreating}
            className={cn("lg-btn-secondary px-4 py-2 text-sm", "disabled:opacity-50")}
          >
            取消
          </button>
          {isGroupMode && (
            <div className="ml-auto flex items-center gap-3">
              <span className="text-xs text-slate-500 dark:text-slate-400">
                已选 {selectedUsers.length} 人
              </span>
              <button
                onClick={handleCreateGroup}
                disabled={isCreating || selectedUsers.length < 2}
                className={cn("lg-btn-primary px-4 py-2 text-sm", "disabled:opacity-50")}
              >
                创建
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
