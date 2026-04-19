import { Link } from "@tanstack/react-router";
import { type SessionRow } from "../../db/schema";
import { GlassCard } from "../../components/GlassCard";
import { useConnectionState } from "../../hooks/useConnectionState";
import { useSessionTimeline } from "../../hooks/useSessionTimeline";

interface SessionDetailPanelProps {
  session: SessionRow | null;
}

export function SessionDetailPanel({ session }: SessionDetailPanelProps) {
  const connection = useConnectionState();
  const timeline = useSessionTimeline(session?.sessionId) ?? [];

  if (session === null) {
    return (
      <GlassCard className="h-full w-full flex items-center justify-center text-center" padding="24px" cornerRadius={24} enableTilt={false}>
        <div className="space-y-3 text-white/45">
          <h3 className="text-lg font-medium text-white/75">Details Panel</h3>
          <p className="text-sm leading-relaxed">选择会话后，这里会展示资料、成员、媒体与快捷操作。</p>
          <div className="flex justify-center gap-2 text-xs">
            <Link to="/contacts" className="rounded-full border border-white/10 px-3 py-1 hover:bg-white/5">Contacts</Link>
            <Link to="/settings" className="rounded-full border border-white/10 px-3 py-1 hover:bg-white/5">Settings</Link>
          </div>
        </div>
      </GlassCard>
    );
  }

  return (
    <GlassCard className="h-full w-full !p-0 flex flex-col" padding="0" cornerRadius={24} enableTilt={false}>
      <div className="px-5 py-5 border-b border-white/5 shrink-0">
        <h3 className="text-lg font-semibold text-white/90">Session Detail</h3>
        <p className="mt-1 text-sm text-white/45">供 UI 层继续扩展的右栏骨架。</p>
      </div>

      <div className="flex-1 overflow-y-auto p-5 space-y-5 custom-scrollbar">
        <section className="space-y-2">
          <p className="text-xs uppercase tracking-[0.22em] text-white/35">Profile</p>
          <div className="rounded-[18px] border border-white/8 bg-white/[0.03] p-4 space-y-2">
            <p className="text-white/90 font-medium">{session.name || session.sessionId}</p>
            <p className="text-sm text-white/45">session_id: {session.sessionId}</p>
            <p className="text-sm text-white/45">type: {session.type}</p>
          </div>
        </section>

        <section className="space-y-2">
          <p className="text-xs uppercase tracking-[0.22em] text-white/35">State</p>
          <div className="rounded-[18px] border border-white/8 bg-white/[0.03] p-4 space-y-2 text-sm text-white/70">
            <p>connection: {connection.status}</p>
            <p>unread: {session.unreadCount}</p>
            <p>timeline items: {timeline.length}</p>
            <p>last event seq: {session.lastEventSeqId}</p>
          </div>
        </section>

        <section className="space-y-2">
          <p className="text-xs uppercase tracking-[0.22em] text-white/35">Planned Actions</p>
          <div className="rounded-[18px] border border-white/8 bg-white/[0.03] p-4 space-y-2 text-sm text-white/55">
            <p>- 成员管理</p>
            <p>- 媒体文件</p>
            <p>- 已读与通知设置</p>
            <p>- 会话元信息编辑</p>
          </div>
        </section>
      </div>
    </GlassCard>
  );
}
