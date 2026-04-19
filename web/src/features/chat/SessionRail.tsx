import { Link } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { Plus, Search, Settings2, Users } from "lucide-react";

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
    <GlassCard className="h-full flex flex-col w-full !p-0" padding="0" cornerRadius={24} enableTilt={false}>
      <div className="px-5 pt-6 pb-4 border-b border-white/10 shrink-0 space-y-4 relative z-10 bg-white/[0.02]">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold text-white tracking-tight">Messages</h2>
          </div>
          <div className="flex gap-2">
            <Link
              to="/contacts"
              className="w-[38px] h-[38px] rounded-full border border-white/15 bg-white/[0.05] text-white/80 flex items-center justify-center hover:bg-white/10 hover:text-white transition-colors shadow-sm"
              title="Contacts"
            >
              <Users className="w-4 h-4" />
            </Link>
            <Link
              to="/contacts"
              className="w-[38px] h-[38px] rounded-full border border-white/15 bg-white/[0.05] text-white/80 flex items-center justify-center hover:bg-white/10 hover:text-white transition-colors shadow-sm"
              title="New Chat / Group"
            >
              <Plus className="w-4 h-4" />
            </Link>
            <Link
              to="/settings"
              className="w-[38px] h-[38px] rounded-full border border-white/15 bg-white/[0.05] text-white/80 flex items-center justify-center hover:bg-white/10 hover:text-white transition-colors shadow-sm"
              title="Settings"
            >
              <Settings2 className="w-4 h-4" />
            </Link>
          </div>
        </div>

        <label className="flex items-center gap-3 rounded-[16px] border border-white/15 bg-black/20 px-4 py-3 text-white/60 focus-within:border-white/30 focus-within:text-white focus-within:bg-black/30 transition-all shadow-inner">
          <Search className="w-[18px] h-[18px] shrink-0" />
          <input
            className="bg-transparent outline-none text-[15px] w-full text-white placeholder:text-white/40"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search chats"
            value={query}
          />
        </label>
      </div>

      <div className="flex-1 overflow-y-auto p-3 space-y-1.5 custom-scrollbar relative z-10">
        {filteredSessions.length === 0 ? (
          <div className="text-center mt-10 space-y-3 px-4">
            <p className="text-[15px] text-white/50">No messages yet.</p>
            <Link to="/contacts" className="inline-flex rounded-full border border-white/20 bg-white/5 px-4 py-2 text-sm text-white hover:bg-white/10 transition-colors">
              Go to Contacts
            </Link>
          </div>
        ) : (
          filteredSessions.map((session) => (
              <Link
                to="/chat/$sessionId"
                params={{ sessionId: session.sessionId }}
                key={session.sessionId}
                className="block w-full rounded-[16px] border px-4 py-3.5 text-left transition-all duration-200 outline-none group border-transparent hover:bg-white/10 hover:border-white/10"
                activeProps={{
                  className: 'bg-[var(--color-primary)] border-[var(--color-primary-border)] shadow-[0_4px_12px_rgba(180,83,60,0.3),inset_0_1px_1px_rgba(255,255,255,0.4)]'
                }}
              >
                {({ isActive }) => (
                  <>
                    <div className="flex justify-between items-center mb-1 gap-2">
                      <span className={`font-semibold truncate text-[16px] ${isActive ? 'text-white' : 'text-white/90 group-hover:text-white'}`}>
                        {session.name || session.sessionId}
                      </span>
                      {Number(session.unreadCount) > 0 ? (
                        <span className={`text-[11px] font-bold px-2 py-0.5 rounded-full shrink-0 shadow-sm ${isActive ? 'bg-white text-[var(--color-primary)]' : 'bg-[var(--color-primary)] text-white'}`}>
                          {session.unreadCount}
                        </span>
                      ) : null}
                    </div>
                    <p className={`text-[14px] truncate ${isActive ? 'text-white/80' : 'text-white/50 group-hover:text-white/70'}`}>
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
