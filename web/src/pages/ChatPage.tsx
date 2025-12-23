import { useEffect, useState } from 'react'
import { useAuthStore } from '@/stores/auth'
import { useSessionStore } from '@/stores/session'
import { useMessageStore } from '@/stores/message'
import { sessionClient } from '@/api/client'
import type { SessionInfo as SessionInfoType } from '@/gen/gateway/v1/api_pb'

export default function ChatPage() {
  const { user, accessToken, logout } = useAuthStore()
  const { sessions, currentSession, setSessions, setCurrentSession } = useSessionStore()
  const { getSessionMessages } = useMessageStore()
  const [isLoading, setIsLoading] = useState(false)

  // Load sessions on mount
  useEffect(() => {
    if (!accessToken) return

    const loadSessions = async () => {
      setIsLoading(true)
      try {
        const response = (await sessionClient.getSessionList({
          accessToken,
        })) as any
        setSessions(
          response.sessions.map((s: SessionInfoType) => ({
            sessionId: s.sessionId,
            userId: s.sessionId, // For now, use sessionId as userId
            userName: s.name,
            userAvatar: s.avatarUrl,
            isGroup: false,
            unreadCount: Number(s.unreadCount),
            lastMessage: s.lastMessage?.content,
            lastMessageTime: s.lastMessage ? Number(s.lastMessage.timestamp) : undefined,
          }))
        )
      } catch (err) {
        console.error('Failed to load sessions:', err)
      } finally {
        setIsLoading(false)
      }
    }

    loadSessions()
  }, [accessToken, setSessions])

  const messages = currentSession ? getSessionMessages(currentSession.sessionId) : []

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="border-b border-border bg-card px-4 py-4">
        <div className="flex items-center justify-between">
          <h1 className="text-2xl font-bold text-foreground">Resonance IM</h1>
          <div className="flex items-center gap-4">
            <span className="text-sm text-muted-foreground">{user?.username}</span>
            <button
              onClick={logout}
              className="rounded-md bg-destructive px-3 py-1 text-sm text-destructive-foreground hover:bg-destructive/90"
            >
              登出
            </button>
          </div>
        </div>
      </div>

      <div className="flex flex-1 overflow-hidden">
        {/* Sessions Sidebar */}
        <div className="w-64 border-r border-border bg-card">
          <div className="border-b border-border p-4">
            <button className="w-full rounded-md bg-primary px-4 py-2 text-primary-foreground hover:bg-primary/90">
              新建对话
            </button>
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center p-4">
              <p className="text-sm text-muted-foreground">加载中...</p>
            </div>
          ) : (
            <div className="overflow-y-auto">
              {sessions.length === 0 ? (
                <p className="p-4 text-center text-sm text-muted-foreground">暂无对话</p>
              ) : (
                sessions.map((session) => (
                  <div
                    key={session.sessionId}
                    onClick={() => setCurrentSession(session)}
                    className={`border-b border-border p-4 cursor-pointer transition-colors ${
                      currentSession?.sessionId === session.sessionId
                        ? 'bg-primary/10'
                        : 'hover:bg-muted'
                    }`}
                  >
                    <div className="flex items-start justify-between">
                      <div className="flex-1 overflow-hidden">
                        <h3 className="font-semibold text-foreground">{session.userName}</h3>
                        <p className="truncate text-sm text-muted-foreground">
                          {session.lastMessage || '暂无消息'}
                        </p>
                      </div>
                      {session.unreadCount > 0 && (
                        <span className="ml-2 inline-flex items-center rounded-full bg-destructive px-2 py-1 text-xs font-semibold text-destructive-foreground">
                          {session.unreadCount}
                        </span>
                      )}
                    </div>
                  </div>
                ))
              )}
            </div>
          )}
        </div>

        {/* Chat Content */}
        <div className="flex-1 flex flex-col">
          {!currentSession ? (
            <div className="flex flex-1 items-center justify-center text-muted-foreground">
              <p>选择一个对话开始聊天</p>
            </div>
          ) : (
            <>
              {/* Chat Header */}
              <div className="border-b border-border bg-card px-6 py-4">
                <h2 className="text-lg font-semibold text-foreground">{currentSession.userName}</h2>
              </div>

              {/* Messages */}
              <div className="flex-1 overflow-y-auto p-6">
                {messages.length === 0 ? (
                  <p className="text-center text-muted-foreground">暂无消息</p>
                ) : (
                  <div className="space-y-4">
                    {messages.map((msg) => (
                      <div
                        key={msg.msgId}
                        className={`flex ${msg.isOwn ? 'justify-end' : 'justify-start'}`}
                      >
                        <div
                          className={`rounded-lg px-4 py-2 max-w-xs ${
                            msg.isOwn
                              ? 'bg-primary text-primary-foreground'
                              : 'bg-muted text-foreground'
                          }`}
                        >
                          <p>{msg.content}</p>
                          <p className="text-xs opacity-70 mt-1">
                            {new Date(msg.timestamp).toLocaleTimeString()}
                          </p>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>

              {/* Input */}
              <div className="border-t border-border bg-card p-4">
                <form className="flex gap-2">
                  <input
                    type="text"
                    placeholder="输入消息..."
                    className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-foreground placeholder-muted-foreground focus:border-primary focus:outline-none"
                  />
                  <button
                    type="submit"
                    className="rounded-md bg-primary px-4 py-2 text-primary-foreground hover:bg-primary/90"
                  >
                    发送
                  </button>
                </form>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
