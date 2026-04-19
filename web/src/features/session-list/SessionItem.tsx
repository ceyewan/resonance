import { Link, useMatchRoute } from "@tanstack/react-router";
import { type SessionRow } from "../../db/schema";

interface SessionItemProps {
  session: SessionRow;
}

export function SessionItem({ session }: SessionItemProps) {
  const matchRoute = useMatchRoute();
  
  // Check if this route is currently active
  const isActive = matchRoute({ to: '/chat/$sessionId', params: { sessionId: session.sessionId } });

  return (
    <Link 
      to="/chat/$sessionId" 
      params={{ sessionId: session.sessionId }} 
      className="block outline-none"
    >
      <div className={`p-3 rounded-[16px] transition-all duration-200 cursor-pointer ${
        isActive 
          ? 'bg-[var(--glass-surface-hover)] shadow-[inset_0_1px_1px_rgba(255,255,255,0.15)] border border-[var(--color-border)]' 
          : 'hover:bg-[var(--glass-surface)] border border-transparent'
      }`}>
        <div className="flex justify-between items-center mb-1">
          <span className="font-medium text-[var(--color-text)] truncate mr-2">
            {session.name || session.sessionId}
          </span>
          {Number(session.unreadCount) > 0 && (
            <span className="bg-[var(--color-primary)] text-[var(--color-text)] text-[11px] font-bold px-2 py-0.5 rounded-full shrink-0 shadow-[0_2px_4px_rgba(0,0,0,0.2)]">
              {session.unreadCount}
            </span>
          )}
        </div>
        <p className={`text-[13px] truncate ${isActive ? 'text-[var(--color-text-muted)]' : 'text-[var(--color-text-muted)] opacity-60'}`}>
          {session.lastEventPreview || "No preview"}
        </p>
      </div>
    </Link>
  );
}
