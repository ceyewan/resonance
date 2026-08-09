import { Link } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { Bot, Plus, Search, Settings2, Users } from "lucide-react";
import { SessionKind } from "@gen/common/v1/session_pb";

import { GlassCard } from "../../components/GlassCard";
import { useSessionListLive } from "../../hooks/useSessionListLive";

export function SessionRail() {
  const sessions = useSessionListLive() ?? [];
  const [query, setQuery] = useState("");

  const filteredSessions = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    if (normalizedQuery === "") {
      return sessions;
    }

    return sessions.filter((session) => {
      const haystacks = [session.name, session.sessionId, session.lastEventPreview];
      return haystacks.some((value) => value.toLowerCase().includes(normalizedQuery));
    });
  }, [query, sessions]);

  return (
    <GlassCard
      className="h-full flex flex-col w-full !p-0"
      padding="0"
      cornerRadius={24}
      enableTilt={false}
    >
      <div className="px-5 pt-6 pb-4 border-b border-[var(--color-border)] shrink-0 space-y-4 relative z-10 bg-[var(--glass-surface)]">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold text-[var(--color-text)] tracking-tight">
              Messages
            </h2>
          </div>
          <div className="flex gap-2">
            <Link
              to="/contacts"
              className="w-[38px] h-[38px] rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] text-[var(--color-text)] opacity-90 flex items-center justify-center hover:bg-[var(--glass-surface-hover)] hover:text-[var(--color-text)] transition-colors shadow-sm"
              title="Contacts"
            >
              <Users className="w-4 h-4" />
            </Link>
            <Link
              to="/contacts"
              className="w-[38px] h-[38px] rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] text-[var(--color-text)] opacity-90 flex items-center justify-center hover:bg-[var(--glass-surface-hover)] hover:text-[var(--color-text)] transition-colors shadow-sm"
              title="New Chat / Group"
            >
              <Plus className="w-4 h-4" />
            </Link>
            <Link
              to="/settings"
              className="w-[38px] h-[38px] rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] text-[var(--color-text)] opacity-90 flex items-center justify-center hover:bg-[var(--glass-surface-hover)] hover:text-[var(--color-text)] transition-colors shadow-sm"
              title="Settings"
            >
              <Settings2 className="w-4 h-4" />
            </Link>
          </div>
        </div>

        <label className="flex items-center gap-3 rounded-[16px] border border-[var(--color-border)] bg-[var(--glass-input-bg)] px-4 py-3 text-[var(--color-text-muted)] opacity-80 focus-within:border-[var(--color-border)] focus-within:text-[var(--color-text)] focus-within:bg-[var(--glass-input-focus-bg)] transition-all shadow-inner">
          <Search className="w-[18px] h-[18px] shrink-0" />
          <input
            className="bg-transparent outline-none text-[15px] w-full text-[var(--color-text)] placeholder:text-[var(--color-text-muted)] opacity-60"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search chats"
            value={query}
          />
        </label>
      </div>

      <div className="flex-1 overflow-y-auto p-3 space-y-1.5 custom-scrollbar relative z-10">
        {filteredSessions.length === 0 ? (
          <div className="text-center mt-10 space-y-3 px-4">
            <p className="text-[15px] text-[var(--color-text-muted)] opacity-70">
              No messages yet.
            </p>
            <Link
              to="/contacts"
              className="inline-flex rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] px-4 py-2 text-sm text-[var(--color-text)] hover:bg-[var(--glass-surface-hover)] transition-colors"
            >
              Go to Contacts
            </Link>
          </div>
        ) : (
          filteredSessions.map((session) => (
            <Link
              to="/chat/$sessionId"
              params={{ sessionId: session.sessionId }}
              key={session.sessionId}
              className="block w-full rounded-[16px] border px-4 py-3.5 text-left transition-all duration-200 outline-none group border-transparent hover:bg-[var(--glass-surface-hover)] hover:border-[var(--color-border)]"
              activeProps={{
                className:
                  "bg-[var(--color-primary)] border-[var(--color-primary-border)] shadow-[0_4px_12px_rgba(180,83,60,0.3),inset_0_1px_1px_rgba(255,255,255,0.4)]",
              }}
            >
              {({ isActive }) => (
                <>
                  <div className="flex justify-between items-center mb-1 gap-2">
                    <span className="min-w-0 flex items-center gap-1.5">
                      <span
                        className={`font-semibold truncate text-[16px] ${isActive ? "text-[var(--color-text)]" : "text-[var(--color-text)] group-hover:text-[var(--color-text)]"}`}
                      >
                        {session.name || session.sessionId}
                      </span>
                      {session.kind === SessionKind.AI ? (
                        <span className="inline-flex shrink-0 items-center gap-1 rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--color-text-muted)]">
                          <Bot className="h-3 w-3" /> BOT
                        </span>
                      ) : null}
                    </span>
                    {Number(session.unreadCount) > 0 ? (
                      <span
                        className={`text-[11px] font-bold px-2 py-0.5 rounded-full shrink-0 shadow-sm ${isActive ? "bg-[var(--glass-surface)] text-[var(--color-primary)]" : "bg-[var(--color-primary)] text-[var(--color-text)]"}`}
                      >
                        {session.unreadCount}
                      </span>
                    ) : null}
                  </div>
                  <p
                    className={`text-[14px] truncate ${isActive ? "text-[var(--color-text)] opacity-90" : "text-[var(--color-text-muted)] opacity-70 group-hover:text-[var(--color-text-muted)]"}`}
                  >
                    {session.lastEventPreview || "No preview"}
                  </p>
                </>
              )}
            </Link>
          ))
        )}
      </div>
    </GlassCard>
  );
}
