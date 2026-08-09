import { type SessionRow } from "../../db/schema";
import { GlassCard } from "../../components/GlassCard";
import { useAuthState } from "../../hooks/useAuthState";
import { useConnectionState } from "../../hooks/useConnectionState";
import { useSessionTimeline } from "../../hooks/useSessionTimeline";
import { AgentProfile, SessionKind, SessionType } from "@gen/common/v1/session_pb";
import { toBigIntId } from "../../lib/id";

function formatSessionReadSummary(
  session: SessionRow,
  currentUsername: string | undefined,
): string {
  if (session.type === SessionType.DIRECT) {
    return toBigIntId(session.readUptoSeqId) > 0n
      ? `对方已读至 #${session.readUptoSeqId}`
      : "对方尚未回执";
  }
  if (session.type === SessionType.GROUP) {
    const readCount = Object.entries(session.readUptoSeqByUser).filter(
      ([username, seqId]) => username !== currentUsername && toBigIntId(seqId) > 0n,
    ).length;
    return readCount > 0 ? `${readCount} 位成员已有读回执` : "暂无成员读回执";
  }
  if (session.kind === SessionKind.AI) {
    return "AI 会话不展示已读回执";
  }
  return "暂无读回执信息";
}

function formatSessionType(sessionType: SessionType): string {
  if (sessionType === SessionType.DIRECT) {
    return "P2P";
  }
  if (sessionType === SessionType.GROUP) {
    return "GROUP";
  }
  if (sessionType === SessionType.AI) {
    return "AI";
  }
  return "UNKNOWN";
}

function formatAgentProfile(profile: AgentProfile): string {
  if (profile === AgentProfile.USER_ASSISTANT) return "USER ASSISTANT";
  if (profile === AgentProfile.IAM_ADMIN) return "IAM ADMIN";
  return "UNSPECIFIED";
}

function formatReadMap(session: SessionRow, currentUsername: string | undefined): string[] {
  return Object.entries(session.readUptoSeqByUser)
    .filter(([username]) => username !== currentUsername)
    .sort((left, right) => right[1].localeCompare(left[1]))
    .map(([username, seqId]) => `${username}: #${seqId}`);
}

interface SessionDetailPanelProps {
  session: SessionRow | null;
}

export function SessionDetailPanel({ session }: SessionDetailPanelProps) {
  const auth = useAuthState();
  const connection = useConnectionState();
  const timeline = useSessionTimeline(session?.sessionId) ?? [];
  const readMap = session ? formatReadMap(session, auth.currentUser?.username) : [];
  const readSummary = session ? formatSessionReadSummary(session, auth.currentUser?.username) : "";

  if (session === null) {
    return (
      <GlassCard
        className="h-full w-full flex items-center justify-center text-center relative overflow-hidden"
        padding="24px"
        cornerRadius={24}
        enableTilt={false}
      >
        <div className="space-y-4 text-[var(--color-text)] relative z-10 px-4">
          <div className="w-[64px] h-[64px] rounded-full bg-[var(--glass-surface)] mx-auto flex items-center justify-center mb-4 border border-[var(--color-border)] shadow-sm">
            <span className="text-[var(--color-text-muted)] opacity-60 text-2xl font-light">i</span>
          </div>
          <h3 className="text-[18px] font-medium text-[var(--color-text)] tracking-tight">
            Details Panel
          </h3>
          <p className="text-[14px] leading-relaxed text-[var(--color-text-muted)] opacity-70 max-w-[240px] mx-auto">
            选择会话后，这里会展示资料、成员、媒体与快捷操作。
          </p>
        </div>
      </GlassCard>
    );
  }

  return (
    <GlassCard
      className="h-full w-full !p-0 flex flex-col relative overflow-hidden"
      padding="0"
      cornerRadius={24}
      enableTilt={false}
    >
      {/* Header */}
      <div className="px-6 py-5 border-b border-[var(--color-border)] shrink-0 bg-[var(--glass-surface)] relative z-10">
        <h3 className="text-[18px] font-semibold text-[var(--color-text)] tracking-tight">
          Session Detail
        </h3>
      </div>

      <div className="flex-1 overflow-y-auto p-5 space-y-6 custom-scrollbar relative z-10">
        <section className="space-y-2.5">
          <p className="text-[11px] uppercase tracking-[0.1em] font-medium text-[var(--color-text-muted)] opacity-60 ml-1">
            Profile
          </p>
          <div className="rounded-[20px] border border-[var(--color-border)] bg-[var(--glass-surface)] p-4 space-y-3 shadow-sm backdrop-blur-md">
            <div className="flex items-center gap-3">
              <div className="w-[44px] h-[44px] rounded-full bg-gradient-to-tr from-blue-500/20 to-purple-500/20 border border-[var(--color-border)] flex items-center justify-center text-[var(--color-text)] font-medium shadow-[inset_0_1px_2px_rgba(255,255,255,0.2)]">
                {(session.name || session.sessionId).charAt(0).toUpperCase()}
              </div>
              <div className="min-w-0 flex-1">
                <p className="text-[var(--color-text)] font-medium text-[15px] truncate">
                  {session.name || session.sessionId}
                </p>
                <p className="text-[13px] text-[var(--color-text-muted)] opacity-70 truncate">
                  ID: {session.sessionId}
                </p>
              </div>
            </div>
            <div className="pt-2 border-t border-[var(--color-border)] flex justify-between items-center">
              <span className="text-[13px] text-[var(--color-text-muted)] opacity-70">Type</span>
              <span className="text-[13px] font-medium text-[var(--color-text)] opacity-90 bg-[var(--glass-surface)] px-2 py-0.5 rounded-md border border-[var(--color-border)]">
                {formatSessionType(session.type)}
              </span>
            </div>
            {session.kind === SessionKind.AI ? (
              <div className="pt-2 border-t border-[var(--color-border)] space-y-2 text-[13px]">
                <div className="flex justify-between gap-3">
                  <span className="text-[var(--color-text-muted)] opacity-70">Agent Profile</span>
                  <span className="text-[var(--color-text)] font-medium">
                    {formatAgentProfile(session.agentProfile)}
                  </span>
                </div>
                <div className="flex justify-between gap-3">
                  <span className="text-[var(--color-text-muted)] opacity-70">Profile Version</span>
                  <span className="text-[var(--color-text)] font-medium">
                    v{session.agentProfileVersion}
                  </span>
                </div>
              </div>
            ) : null}
          </div>
        </section>

        <section className="space-y-2.5">
          <p className="text-[11px] uppercase tracking-[0.1em] font-medium text-[var(--color-text-muted)] opacity-60 ml-1">
            State
          </p>
          <div className="rounded-[20px] border border-[var(--color-border)] bg-[var(--glass-surface)] p-4 space-y-3 text-[14px] text-[var(--color-text-muted)] shadow-sm backdrop-blur-md">
            <div className="flex justify-between items-center">
              <span className="text-[var(--color-text-muted)] opacity-70">Connection</span>
              <span className="flex items-center gap-1.5 text-[var(--color-text)]">
                <span
                  className={`w-2 h-2 rounded-full ${connection.status === "open" ? "bg-green-400" : "bg-red-400"}`}
                ></span>
                {connection.status}
              </span>
            </div>
            <div className="flex justify-between items-center">
              <span className="text-[var(--color-text-muted)] opacity-70">Unread</span>
              <span className="text-[var(--color-text)] font-medium">{session.unreadCount}</span>
            </div>
            <div className="flex justify-between items-center">
              <span className="text-[var(--color-text-muted)] opacity-70">Timeline Items</span>
              <span className="text-[var(--color-text)] font-medium">{timeline.length}</span>
            </div>
            <div className="flex justify-between items-center gap-4">
              <span className="text-[var(--color-text-muted)] opacity-70">Read Status</span>
              <span className="text-[var(--color-text)] font-medium text-right">{readSummary}</span>
            </div>
            {session.type === SessionType.GROUP && readMap.length > 0 ? (
              <div className="pt-2 border-t border-[var(--color-border)] space-y-1.5">
                {readMap.map((item) => (
                  <div
                    key={item}
                    className="flex justify-between items-center text-[12px] text-[var(--color-text-muted)]"
                  >
                    <span>{item}</span>
                  </div>
                ))}
              </div>
            ) : null}
          </div>
        </section>

        <section className="space-y-2.5">
          <p className="text-[11px] uppercase tracking-[0.1em] font-medium text-[var(--color-text-muted)] opacity-60 ml-1">
            Actions
          </p>
          <div className="rounded-[20px] border border-[var(--color-border)] bg-[var(--glass-surface)] p-2 flex flex-col shadow-sm backdrop-blur-md">
            <button className="flex items-center gap-3 px-3 py-2.5 hover:bg-[var(--glass-surface)] rounded-[14px] text-[var(--color-text)] opacity-90 transition-colors text-[14px] font-medium">
              <div className="w-8 h-8 rounded-full bg-[var(--glass-surface)] flex items-center justify-center text-[var(--color-text-muted)] opacity-80">
                👤
              </div>
              Manage Members
            </button>
            <button className="flex items-center gap-3 px-3 py-2.5 hover:bg-[var(--glass-surface)] rounded-[14px] text-[var(--color-text)] opacity-90 transition-colors text-[14px] font-medium">
              <div className="w-8 h-8 rounded-full bg-[var(--glass-surface)] flex items-center justify-center text-[var(--color-text-muted)] opacity-80">
                🖼️
              </div>
              Shared Media
            </button>
            <button className="flex items-center gap-3 px-3 py-2.5 hover:bg-[var(--glass-surface)] rounded-[14px] text-[#ef4444]/80 transition-colors text-[14px] font-medium mt-1">
              <div className="w-8 h-8 rounded-full bg-red-400/10 flex items-center justify-center text-[#ef4444]/70 border border-red-400/10">
                🗑️
              </div>
              Clear History
            </button>
          </div>
        </section>
      </div>
    </GlassCard>
  );
}
