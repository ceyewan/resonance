import { Link } from "@tanstack/react-router";
import { MessageCircle, Plus, Users } from "lucide-react";
import { GlassCard } from "../../components/GlassCard";
import { SessionDetailPanel } from "../session-detail/SessionDetailPanel";

export function EmptyChat() {
  return (
    <div className="grid h-full w-full gap-4 lg:grid-cols-[minmax(0,1fr)_320px] xl:grid-cols-[minmax(0,1fr)_360px]">
      <GlassCard className="h-full flex items-center justify-center w-full relative overflow-hidden" padding="24px" cornerRadius={24} enableTilt={false}>
        <div className="text-center text-white flex flex-col items-center max-w-sm relative z-10">
          <div className="w-[88px] h-[88px] rounded-full bg-white/[0.03] flex items-center justify-center mb-6 backdrop-blur-xl border border-white/10 shadow-[0_8px_32px_rgba(0,0,0,0.1),inset_0_1px_2px_rgba(255,255,255,0.2)]">
            <MessageCircle className="w-10 h-10 text-white/70" strokeWidth={1.5} />
          </div>
          <h3 className="text-[22px] font-semibold text-white mb-2 tracking-tight">Your Messages</h3>
          <p className="text-[15px] leading-relaxed mb-8 text-white/60">Select a chat from the left panel to start messaging, or start a new conversation.</p>
          <div className="flex flex-wrap justify-center gap-3">
            <Link to="/contacts" className="inline-flex items-center gap-2 rounded-full border border-white/15 bg-white/[0.05] px-5 py-2.5 text-[14px] font-medium text-white/90 hover:bg-white/10 hover:text-white transition-all shadow-sm">
              <Users className="w-[18px] h-[18px]" />
              Contacts
            </Link>
            <Link to="/contacts" className="inline-flex items-center gap-2 rounded-full border border-[var(--glass-btn-primary-border)] bg-[var(--color-primary)] px-5 py-2.5 text-[14px] font-medium text-white hover:bg-[var(--color-primary-hover)] transition-all shadow-[0_4px_12px_rgba(180,83,60,0.25),inset_0_1px_1px_rgba(255,255,255,0.3)]">
              <Plus className="w-[18px] h-[18px]" />
              New Chat
            </Link>
          </div>
        </div>
      </GlassCard>

      <aside className="hidden lg:block h-full">
        <SessionDetailPanel session={null} />
      </aside>
    </div>
  );
}
