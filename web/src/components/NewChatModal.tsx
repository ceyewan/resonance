import { useState, useCallback, useEffect } from "react";
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
 * - 单聊：搜索用户（添加新联系人）
 * - 群聊：从联系人列表多选 + 输入群名
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

  // 搜索用户
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
      // 过滤掉当前用户
      setSearchResults(result.filter((u) => u.username !== currentUsername));
    } catch (err) {
      console.error("[NewChatModal] Failed to search users:", err);
      setError("搜索用户失败");
    } finally {
      setIsSearching(false);
    }
  }, [searchQuery, currentUsername]);

  // 搜索防抖
  useEffect(() => {
    if (mode === "single") {
      const timer = setTimeout(() => {
        handleSearch();
      }, 300);
      return () => clearTimeout(timer);
    }
  }, [mode, searchQuery, handleSearch]);

  // 加载联系人列表（群聊模式）
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

  // 切换到群聊模式时加载联系人
  useEffect(() => {
    if (mode === "group" && contacts.length === 0) {
      loadContacts();
    }
  }, [mode, contacts.length, loadContacts]);

  // 切换模式时重置状态
  useEffect(() => {
    setSelectedUsers([]);
    setGroupName("");
    setSearchQuery("");
    setSearchResults([]);
    setError(null);
  }, [mode]);

  // 关闭弹窗
  const handleClose = useCallback(() => {
    setSelectedUsers([]);
    setGroupName("");
    setSearchQuery("");
    setSearchResults([]);
    setError(null);
    setMode("single");
    onClose();
  }, [onClose]);

  // 切换用户选择
  const toggleUser = useCallback(
    (username: string) => {
      if (mode === "single") {
        // 单聊：直接创建
        quickStartChat(username);
        return;
      }
      // 群聊：多选
      setSelectedUsers((prev) => {
        if (prev.includes(username)) {
          return prev.filter((u) => u !== username);
        }
        return [...prev, username];
      });
      setError(null);
    },
    [mode],
  );

  // 快速开始单聊
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
    [onSessionCreated, handleClose],
  );

  // 创建群聊
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

  if (!isOpen) return null;

  const isGroupMode = mode === "group";
  const displayList = isGroupMode ? contacts : searchResults;
  const isLoading = isGroupMode ? isLoadingContacts : isSearching;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div
        className="w-full max-w-md rounded-2xl bg-white shadow-xl dark:bg-gray-800"
        onClick={(e) => e.stopPropagation()}
      >
        {/* 头部 */}
        <div className="flex items-center justify-between border-b border-gray-200 p-4 dark:border-gray-700">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
            新建聊天
          </h2>
          <button
            onClick={handleClose}
            className="rounded-full p-1 text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-300"
            disabled={isCreating}
          >
            <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* 模式切换 */}
        <div className="flex border-b border-gray-200 dark:border-gray-700">
          <button
            onClick={() => setMode("single")}
            disabled={isCreating}
            className={cn(
              "flex-1 px-4 py-3 text-sm font-medium transition-colors",
              mode === "single"
                ? "border-b-2 border-sky-500 text-sky-600 dark:text-sky-400"
                : "text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300",
              "disabled:opacity-50",
            )}
          >
            单聊
          </button>
          <button
            onClick={() => setMode("group")}
            disabled={isCreating}
            className={cn(
              "flex-1 px-4 py-3 text-sm font-medium transition-colors",
              mode === "group"
                ? "border-b-2 border-sky-500 text-sky-600 dark:text-sky-400"
                : "text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300",
              "disabled:opacity-50",
            )}
          >
            群聊
          </button>
        </div>

        {/* 搜索框（单聊模式）或过滤框（群聊模式） */}
        <div className="p-4">
          <div className="relative">
            <svg
              className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
              />
            </svg>
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder={isGroupMode ? "搜索联系人" : "搜索用户名或昵称"}
              disabled={isCreating}
              className={cn(
                "w-full rounded-lg border border-gray-300 bg-white py-2 pl-10 pr-4 text-sm",
                "placeholder-gray-400 focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20",
                "dark:border-gray-600 dark:bg-gray-700 dark:text-white dark:placeholder-gray-500",
                "disabled:opacity-50",
              )}
              autoFocus
            />
          </div>
        </div>

        {/* 用户列表 */}
        <div className="max-h-72 overflow-y-auto px-2">
          {isLoading ? (
            <div className="flex items-center justify-center p-8">
              <svg className="h-5 w-5 animate-spin text-gray-400" fill="none" viewBox="0 0 24 24">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path
                  className="opacity-75"
                  fill="currentColor"
                  d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                />
              </svg>
            </div>
          ) : displayList.length === 0 ? (
            <div className="flex flex-col items-center p-8 text-center">
              <svg
                className="mb-3 h-12 w-12 text-gray-400"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={1.5}
                  d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z"
                />
              </svg>
              <p className="text-sm text-gray-500 dark:text-gray-400">
                {isGroupMode
                  ? (contacts.length === 0 ? "暂无联系人" : "未找到匹配的联系人")
                  : (searchQuery ? "未找到用户" : "输入搜索关键词")
                }
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
                      "flex cursor-pointer items-center gap-3 rounded-lg p-3 transition-colors",
                      "hover:bg-gray-100 dark:hover:bg-gray-700",
                      isCreating && "opacity-50 cursor-not-allowed",
                    )}
                  >
                    {/* 复选框（仅群聊模式） */}
                    {isGroupMode && (
                      <div className={cn(
                        "flex h-5 w-5 shrink-0 items-center justify-center rounded border",
                        isSelected
                          ? "border-sky-500 bg-sky-500"
                          : "border-gray-300 dark:border-gray-600",
                      )}>
                        {isSelected && (
                          <svg className="h-3 w-3 text-white" fill="currentColor" viewBox="0 0 20 20">
                            <path
                              fillRule="evenodd"
                              d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
                              clipRule="evenodd"
                            />
                          </svg>
                        )}
                      </div>
                    )}

                    {/* 头像 */}
                    <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-sky-400 to-sky-600 text-sm font-semibold text-white">
                      {(user.nickname || user.username).charAt(0).toUpperCase()}
                    </div>

                    {/* 用户信息 */}
                    <div className="flex-1 min-w-0">
                      <p className="truncate text-sm font-medium text-gray-900 dark:text-white">
                        {user.nickname || user.username}
                      </p>
                      <p className="truncate text-xs text-gray-500 dark:text-gray-400">
                        @{user.username}
                      </p>
                    </div>

                    {/* 单聊模式箭头 */}
                    {!isGroupMode && (
                      <svg className="h-5 w-5 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
                      </svg>
                    )}

                    {/* 群聊模式加载中 */}
                    {isGroupMode && isCreating && isSelected && (
                      <svg className="h-5 w-5 animate-spin text-sky-500" fill="none" viewBox="0 0 24 24">
                        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                        <path
                          className="opacity-75"
                          fill="currentColor"
                          d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                        />
                      </svg>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* 群名输入（仅群聊模式） */}
        {isGroupMode && (
          <div className="border-t border-gray-200 p-4 dark:border-gray-700">
            <input
              type="text"
              value={groupName}
              onChange={(e) => setGroupName(e.target.value)}
              placeholder="输入群名"
              disabled={isCreating}
              className={cn(
                "w-full rounded-lg border border-gray-300 bg-white px-4 py-2 text-sm",
                "placeholder-gray-400 focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20",
                "dark:border-gray-600 dark:bg-gray-700 dark:text-white dark:placeholder-gray-500",
                "disabled:opacity-50",
              )}
            />
          </div>
        )}

        {/* 错误提示 */}
        {error && (
          <div className="px-4 pb-2">
            <p className="text-sm text-red-500">{error}</p>
          </div>
        )}

        {/* 底部操作按钮 */}
        <div className="flex border-t border-gray-200 p-4 dark:border-gray-700">
          <button
            onClick={handleClose}
            disabled={isCreating}
            className={cn(
              "rounded-lg px-4 py-2 text-sm font-medium transition-colors",
              "text-gray-700 hover:bg-gray-100",
              "dark:text-gray-300 dark:hover:bg-gray-700",
              "disabled:opacity-50",
            )}
          >
            取消
          </button>
          {isGroupMode && (
            <div className="ml-auto flex items-center gap-2">
              <span className="text-xs text-gray-500 dark:text-gray-400">
                已选 {selectedUsers.length} 人
              </span>
              <button
                onClick={handleCreateGroup}
                disabled={isCreating || selectedUsers.length < 2}
                className={cn(
                  "rounded-lg px-4 py-2 text-sm font-medium text-white transition-colors",
                  "bg-sky-500 hover:bg-sky-600",
                  "disabled:opacity-50 disabled:hover:bg-sky-500",
                )}
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
