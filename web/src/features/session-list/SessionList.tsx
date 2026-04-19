import { Link } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { Plus, Search, Settings2, Users } from "lucide-react";
import { GlassCard } from "../../components/GlassCard";
import { SessionItem } from "./SessionItem";
import { useSessionListLive } from "../../hooks/useSessionListLive";

export function SessionList() {
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
    <GlassCard className="h-full flex flex-col w-full !p-0" padding="0" cornerRadius={24} enableTilt={false}>
      <div className="px-5 pt-6 pb-4 border-b border-[var(--color-border)] shrink-0 space-y-4">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold text-[var(--color-text)] tracking-tight">Messages</h2>
            <p className="mt-1 text-sm text-[var(--color-text-muted)] opacity-60">会话、联系人与后续拓展入口都从这里进入。</p>
          </div>
          <div className="flex gap-2">
            <Link
              to="/contacts"
              className="w-10 h-10 rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] text-[var(--color-text-muted)] flex items-center justify-center hover:bg-[var(--glass-surface-hover)]"
              title="Contacts"
            >
              <Users className="w-4 h-4" />
            </Link>
            <Link
              to="/contacts"
              className="w-10 h-10 rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] text-[var(--color-text-muted)] flex items-center justify-center hover:bg-[var(--glass-surface-hover)]"
              title="New Chat / Group"
            >
              <Plus className="w-4 h-4" />
            </Link>
            <Link
              to="/settings"
              className="w-10 h-10 rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] text-[var(--color-text-muted)] flex items-center justify-center hover:bg-[var(--glass-surface-hover)]"
              title="Settings"
            >
              <Settings2 className="w-4 h-4" />
            </Link>
          </div>
        </div>

        <label className="flex items-center gap-3 rounded-[16px] border border-[var(--color-border)] bg-[var(--glass-surface)] px-4 py-3 text-[var(--color-text-muted)] opacity-70 focus-within:border-[var(--color-border)]">
          <Search className="w-4 h-4 shrink-0" />
          <input
            className="bg-transparent outline-none text-sm w-full placeholder:text-[var(--color-text-muted)] opacity-50"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search chats"
            value={query}
          />
        </label>
      </div>

      <div className="flex-1 overflow-y-auto p-3 space-y-1 custom-scrollbar">
        {filteredSessions.length === 0 ? (
          <div className="text-center mt-10 space-y-3 px-4">
            <p className="text-sm text-[var(--color-text-muted)] opacity-60">No messages yet.</p>
            <Link to="/contacts" className="inline-flex rounded-full border border-[var(--color-border)] px-3 py-1.5 text-xs text-[var(--color-text-muted)] hover:bg-[var(--glass-surface)]">
              Go to Contacts
            </Link>
          </div>
        ) : (
          filteredSessions.map((session) => (
            <SessionItem key={session.sessionId} session={session} />
          ))
        )}
      </div>
    </GlassCard>
  );
}
