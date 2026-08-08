import { useNavigate } from "@tanstack/react-router";
import { GlassCard } from "../../components/GlassCard";
import { WallpaperBackground } from "../../components/WallpaperBackground";
import { useAuthGuard } from "../../hooks/useAuthGuard";
import { useConnectionState } from "../../hooks/useConnectionState";
import { logout } from "../../services/auth";

export function SettingsPage() {
  const auth = useAuthGuard();
  const connection = useConnectionState();
  const navigate = useNavigate();
  const approvalEntryHint =
    auth.roles.includes("iam-admin") &&
    (auth.scopes.includes("agent:approval:read") || auth.scopes.includes("agent:approval:decide"));

  if (auth.bootstrapping) {
    return (
      <WallpaperBackground>
        <div className="flex h-screen items-center justify-center text-[var(--color-text-muted)] opacity-70 text-sm">
          Loading...
        </div>
      </WallpaperBackground>
    );
  }

  return (
    <WallpaperBackground>
      <main className="mx-auto flex h-screen w-full max-w-[800px] items-center justify-center p-6">
        <GlassCard
          className="w-full relative overflow-hidden"
          padding="40px"
          cornerRadius={32}
          enableTilt={false}
        >
          <div className="flex items-start justify-between gap-4 relative z-10">
            <div>
              <h1 className="text-[28px] font-semibold text-[var(--color-text)] tracking-tight">
                Settings
              </h1>
              <p className="mt-1.5 text-[15px] text-[var(--color-text-muted)] opacity-80">
                供 UI 层继续扩展头像、昵称、通知、主题与退出登录等设置项。
              </p>
            </div>
            <button
              className="rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] px-4 py-2 text-[14px] font-medium text-[var(--color-text)] hover:bg-[var(--glass-surface-hover)] transition-colors shadow-sm"
              onClick={() => void navigate({ to: "/chat" })}
              type="button"
            >
              Back to Chat
            </button>
          </div>

          <div className="mt-10 grid gap-5 md:grid-cols-2 relative z-10">
            <div className="rounded-[24px] border border-[var(--color-border)] bg-[var(--glass-surface)] p-6 space-y-3 shadow-sm backdrop-blur-md">
              <p className="text-[12px] uppercase tracking-[0.15em] font-medium text-[var(--color-text-muted)] opacity-70 ml-1">
                Profile
              </p>
              <div className="flex items-center gap-4 mt-2">
                <div className="w-[56px] h-[56px] rounded-full bg-gradient-to-tr from-blue-500/30 to-purple-500/30 border border-[var(--color-border)] flex items-center justify-center text-[var(--color-text)] font-medium text-xl shadow-[inset_0_1px_2px_rgba(255,255,255,0.3)]">
                  {(auth.currentUser?.nickname || auth.currentUser?.username || "?")
                    .charAt(0)
                    .toUpperCase()}
                </div>
                <div>
                  <p className="text-[var(--color-text)] font-medium text-[17px]">
                    {auth.currentUser?.nickname || auth.currentUser?.username || "Guest"}
                  </p>
                  <p className="text-[14px] text-[var(--color-text-muted)] opacity-80">
                    @{auth.currentUser?.username || "-"}
                  </p>
                </div>
              </div>
            </div>

            <div className="rounded-[24px] border border-[var(--color-border)] bg-[var(--glass-surface)] p-6 space-y-3 shadow-sm backdrop-blur-md">
              <p className="text-[12px] uppercase tracking-[0.15em] font-medium text-[var(--color-text-muted)] opacity-70 ml-1">
                Runtime Status
              </p>
              <div className="mt-2 space-y-2.5 text-[15px] text-[var(--color-text)] opacity-90">
                <div className="flex justify-between items-center border-b border-[var(--color-border)] pb-2.5">
                  <span className="text-[var(--color-text-muted)] opacity-80">Connection</span>
                  <span className="flex items-center gap-1.5 font-medium text-[var(--color-text)]">
                    <span
                      className={`w-2 h-2 rounded-full ${connection.status === "open" ? "bg-green-400" : "bg-red-400"}`}
                    ></span>
                    {connection.status}
                  </span>
                </div>
                <div className="flex justify-between items-center pt-0.5">
                  <span className="text-[var(--color-text-muted)] opacity-80">Inbox Syncing</span>
                  <span className="font-medium text-[var(--color-text)]">
                    {connection.inboxSyncing ? "Syncing..." : "Idle"}
                  </span>
                </div>
              </div>
            </div>
          </div>

          <div className="mt-10 pt-8 border-t border-[var(--color-border)] flex flex-wrap gap-4 relative z-10">
            {approvalEntryHint ? (
              <button
                className="rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] px-6 py-2.5 text-[15px] font-medium text-[var(--color-text)] hover:bg-[var(--glass-surface-hover)] transition-colors shadow-sm"
                onClick={() => void navigate({ to: "/approvals" })}
                title="本地权限仅用于显示入口，服务端会重新校验"
                type="button"
              >
                Agent Approvals
              </button>
            ) : null}
            <button
              className="rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] px-6 py-2.5 text-[15px] font-medium text-[var(--color-text)] hover:bg-[var(--glass-surface-hover)] transition-colors shadow-sm"
              onClick={() => void navigate({ to: "/contacts" })}
              type="button"
            >
              Go to Contacts
            </button>
            <button
              className="rounded-full border border-red-400/30 bg-red-400/10 px-6 py-2.5 text-[15px] font-medium text-[#fca5a5] dark:text-[#fecaca] hover:bg-red-400/20 transition-colors shadow-sm"
              onClick={() => {
                void (async () => {
                  await logout();
                  void navigate({ to: "/login" });
                })();
              }}
              type="button"
            >
              Logout
            </button>
          </div>
        </GlassCard>
      </main>
    </WallpaperBackground>
  );
}
