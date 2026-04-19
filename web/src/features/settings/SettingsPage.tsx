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

  if (auth.bootstrapping) {
    return (
      <WallpaperBackground>
        <div className="flex h-screen items-center justify-center text-white/50 text-sm">Loading...</div>
      </WallpaperBackground>
    );
  }

  return (
    <WallpaperBackground>
      <main className="mx-auto flex h-screen w-full max-w-[800px] items-center justify-center p-6">
        <GlassCard className="w-full relative overflow-hidden" padding="40px" cornerRadius={32} enableTilt={false}>
          <div className="flex items-start justify-between gap-4 relative z-10">
            <div>
              <h1 className="text-[28px] font-semibold text-white tracking-tight">Settings</h1>
              <p className="mt-1.5 text-[15px] text-white/60">
                供 UI 层继续扩展头像、昵称、通知、主题与退出登录等设置项。
              </p>
            </div>
            <button
              className="rounded-full border border-white/15 bg-white/5 px-4 py-2 text-[14px] font-medium text-white hover:bg-white/10 transition-colors shadow-sm"
              onClick={() => void navigate({ to: "/chat" })}
              type="button"
            >
              Back to Chat
            </button>
          </div>

          <div className="mt-10 grid gap-5 md:grid-cols-2 relative z-10">
            <div className="rounded-[24px] border border-white/10 bg-white/[0.04] p-6 space-y-3 shadow-sm backdrop-blur-md">
              <p className="text-[12px] uppercase tracking-[0.15em] font-medium text-white/50 ml-1">Profile</p>
              <div className="flex items-center gap-4 mt-2">
                <div className="w-[56px] h-[56px] rounded-full bg-gradient-to-tr from-blue-500/30 to-purple-500/30 border border-white/20 flex items-center justify-center text-white font-medium text-xl shadow-[inset_0_1px_2px_rgba(255,255,255,0.3)]">
                  {(auth.currentUser?.nickname || auth.currentUser?.username || "?").charAt(0).toUpperCase()}
                </div>
                <div>
                  <p className="text-white font-medium text-[17px]">{auth.currentUser?.nickname || auth.currentUser?.username || "Guest"}</p>
                  <p className="text-[14px] text-white/60">@{auth.currentUser?.username || "-"}</p>
                </div>
              </div>
            </div>

            <div className="rounded-[24px] border border-white/10 bg-white/[0.04] p-6 space-y-3 shadow-sm backdrop-blur-md">
              <p className="text-[12px] uppercase tracking-[0.15em] font-medium text-white/50 ml-1">Runtime Status</p>
              <div className="mt-2 space-y-2.5 text-[15px] text-white/80">
                <div className="flex justify-between items-center border-b border-white/5 pb-2.5">
                  <span className="text-white/60">Connection</span>
                  <span className="flex items-center gap-1.5 font-medium text-white">
                    <span className={`w-2 h-2 rounded-full ${connection.status === 'open' ? 'bg-green-400' : 'bg-red-400'}`}></span>
                    {connection.status}
                  </span>
                </div>
                <div className="flex justify-between items-center pt-0.5">
                  <span className="text-white/60">Inbox Syncing</span>
                  <span className="font-medium text-white">{connection.inboxSyncing ? "Syncing..." : "Idle"}</span>
                </div>
              </div>
            </div>
          </div>

          <div className="mt-10 pt-8 border-t border-white/10 flex flex-wrap gap-4 relative z-10">
            <button
              className="rounded-full border border-white/15 bg-white/5 px-6 py-2.5 text-[15px] font-medium text-white hover:bg-white/10 transition-colors shadow-sm"
              onClick={() => void navigate({ to: "/contacts" })}
              type="button"
            >
              Go to Contacts
            </button>
            <button
              className="rounded-full border border-red-400/30 bg-red-400/10 px-6 py-2.5 text-[15px] font-medium text-red-200 hover:bg-red-400/20 transition-colors shadow-sm"
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
