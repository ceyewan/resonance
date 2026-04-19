import { Link } from "@tanstack/react-router";
import { MessageCircle, Plus, Users } from "lucide-react";
import { GlassCard } from "../../components/GlassCard";
import { SessionDetailPanel } from "../session-detail/SessionDetailPanel";

export function EmptyChat() {
  return (
    <div className="grid h-full w-full gap-4 lg:grid-cols-[minmax(0,1fr)_320px] xl:grid-cols-[minmax(0,1fr)_360px]">
      <GlassCard className="h-full flex items-center justify-center w-full" padding="24px" cornerRadius={24} enableTilt={false}>
        <div className="text-center text-white/40 flex flex-col items-center max-w-sm">
          <div className="w-20 h-20 rounded-full bg-white/5 flex items-center justify-center mb-6 backdrop-blur-sm border border-white/10 shadow-[inset_0_1px_1px_rgba(255,255,255,0.1),0_8px_32px_rgba(0,0,0,0.1)]">
            <MessageCircle className="w-10 h-10 text-white/50" strokeWidth={1.5} />
          </div>
          <h3 className="text-xl font-medium text-white/80 mb-2 tracking-tight">Your Messages</h3>
          <p className="text-[15px] leading-relaxed mb-5">Select a chat from the left panel to start messaging, or start a new conversation.</p>
          <div className="flex flex-wrap justify-center gap-2">
            <Link to="/contacts" className="inline-flex items-center gap-2 rounded-full border border-white/10 px-4 py-2 text-sm text-white/75 hover:bg-white/5">
              <Users className="w-4 h-4" />
              Contacts
            </Link>
            <Link to="/contacts" className="inline-flex items-center gap-2 rounded-full border border-white/10 px-4 py-2 text-sm text-white/75 hover:bg-white/5">
              <Plus className="w-4 h-4" />
              New Chat / Group
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
