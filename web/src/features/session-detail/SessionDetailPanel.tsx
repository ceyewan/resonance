import { type SessionRow } from "../../db/schema";
import { GlassCard } from "../../components/GlassCard";
import { useConnectionState } from "../../hooks/useConnectionState";
import { useSessionTimeline } from "../../hooks/useSessionTimeline";
import { SessionType } from "@gen/common/v1/session_pb";

interface SessionDetailPanelProps {
  session: SessionRow | null;
}

export function SessionDetailPanel({ session }: SessionDetailPanelProps) {
  const connection = useConnectionState();
  const timeline = useSessionTimeline(session?.sessionId) ?? [];

  if (session === null) {
    return (
      <GlassCard className="h-full w-full flex items-center justify-center text-center relative overflow-hidden" padding="24px" cornerRadius={24} enableTilt={false}>
        <div className="space-y-4 text-white relative z-10 px-4">
          <div className="w-[64px] h-[64px] rounded-full bg-white/[0.03] mx-auto flex items-center justify-center mb-4 border border-white/10 shadow-sm">
            <span className="text-white/40 text-2xl font-light">i</span>
          </div>
          <h3 className="text-[18px] font-medium text-white/90 tracking-tight">Details Panel</h3>
          <p className="text-[14px] leading-relaxed text-white/50 max-w-[240px] mx-auto">选择会话后，这里会展示资料、成员、媒体与快捷操作。</p>
        </div>
      </GlassCard>
    );
  }

  return (
    <GlassCard className="h-full w-full !p-0 flex flex-col relative overflow-hidden" padding="0" cornerRadius={24} enableTilt={false}>
      {/* Header */}
      <div className="px-6 py-5 border-b border-white/10 shrink-0 bg-white/[0.02] relative z-10">
        <h3 className="text-[18px] font-semibold text-white tracking-tight">Session Detail</h3>
      </div>

      <div className="flex-1 overflow-y-auto p-5 space-y-6 custom-scrollbar relative z-10">
        <section className="space-y-2.5">
          <p className="text-[11px] uppercase tracking-[0.1em] font-medium text-white/40 ml-1">Profile</p>
          <div className="rounded-[20px] border border-white/10 bg-white/[0.03] p-4 space-y-3 shadow-sm backdrop-blur-md">
            <div className="flex items-center gap-3">
              <div className="w-[44px] h-[44px] rounded-full bg-gradient-to-tr from-blue-500/20 to-purple-500/20 border border-white/10 flex items-center justify-center text-white font-medium shadow-[inset_0_1px_2px_rgba(255,255,255,0.2)]">
                {(session.name || session.sessionId).charAt(0).toUpperCase()}
              </div>
              <div className="min-w-0 flex-1">
                <p className="text-white font-medium text-[15px] truncate">{session.name || session.sessionId}</p>
                <p className="text-[13px] text-white/50 truncate">ID: {session.sessionId}</p>
              </div>
            </div>
            <div className="pt-2 border-t border-white/5 flex justify-between items-center">
              <span className="text-[13px] text-white/50">Type</span>
              <span className="text-[13px] font-medium text-white/80 bg-white/5 px-2 py-0.5 rounded-md border border-white/5">{session.type === SessionType.DIRECT ? 'P2P' : session.type === SessionType.GROUP ? 'GROUP' : 'UNKNOWN'}</span>
            </div>
          </div>
        </section>

        <section className="space-y-2.5">
          <p className="text-[11px] uppercase tracking-[0.1em] font-medium text-white/40 ml-1">State</p>
          <div className="rounded-[20px] border border-white/10 bg-white/[0.03] p-4 space-y-3 text-[14px] text-white/70 shadow-sm backdrop-blur-md">
            <div className="flex justify-between items-center">
              <span className="text-white/50">Connection</span>
              <span className="flex items-center gap-1.5 text-white/90">
                <span className={`w-2 h-2 rounded-full ${connection.status === 'open' ? 'bg-green-400' : 'bg-red-400'}`}></span>
                {connection.status}
              </span>
            </div>
            <div className="flex justify-between items-center">
              <span className="text-white/50">Unread</span>
              <span className="text-white/90 font-medium">{session.unreadCount}</span>
            </div>
            <div className="flex justify-between items-center">
              <span className="text-white/50">Timeline Items</span>
              <span className="text-white/90 font-medium">{timeline.length}</span>
            </div>
          </div>
        </section>

        <section className="space-y-2.5">
          <p className="text-[11px] uppercase tracking-[0.1em] font-medium text-white/40 ml-1">Actions</p>
          <div className="rounded-[20px] border border-white/10 bg-white/[0.03] p-2 flex flex-col shadow-sm backdrop-blur-md">
            <button className="flex items-center gap-3 px-3 py-2.5 hover:bg-white/5 rounded-[14px] text-white/80 transition-colors text-[14px] font-medium">
              <div className="w-8 h-8 rounded-full bg-white/5 flex items-center justify-center text-white/60">👤</div>
              Manage Members
            </button>
            <button className="flex items-center gap-3 px-3 py-2.5 hover:bg-white/5 rounded-[14px] text-white/80 transition-colors text-[14px] font-medium">
              <div className="w-8 h-8 rounded-full bg-white/5 flex items-center justify-center text-white/60">🖼️</div>
              Shared Media
            </button>
            <button className="flex items-center gap-3 px-3 py-2.5 hover:bg-white/5 rounded-[14px] text-red-400/80 transition-colors text-[14px] font-medium mt-1">
              <div className="w-8 h-8 rounded-full bg-red-400/10 flex items-center justify-center text-red-400/70 border border-red-400/10">🗑️</div>
              Clear History
            </button>
          </div>
        </section>
      </div>
    </GlassCard>
  );
}
