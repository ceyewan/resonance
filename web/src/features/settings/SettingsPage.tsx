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
        <div className="flex h-screen items-center justify-center text-white/45 text-sm">Loading...</div>
      </WallpaperBackground>
    );
  }

  return (
    <WallpaperBackground>
      <main className="mx-auto flex h-screen w-full max-w-[1200px] items-center justify-center p-6">
        <GlassCard className="w-full max-w-3xl" padding="32px" cornerRadius={24} enableTilt={false}>
          <div className="flex items-start justify-between gap-4">
            <div>
              <h1 className="text-2xl font-semibold text-white/90 tracking-tight">Settings Skeleton</h1>
              <p className="mt-2 text-sm text-white/45">
                供 UI 层继续扩展头像、昵称、通知、主题与退出登录等设置项。
              </p>
            </div>
            <button
              className="rounded-full border border-white/10 px-3 py-1.5 text-xs text-white/70 hover:bg-white/5"
              onClick={() => void navigate({ to: "/chat" })}
              type="button"
            >
              Back to Chat
            </button>
          </div>

          <div className="mt-8 grid gap-4 md:grid-cols-2">
            <div className="rounded-[18px] border border-white/8 bg-white/[0.03] p-5 space-y-2">
              <p className="text-xs uppercase tracking-[0.22em] text-white/35">Profile</p>
              <p className="text-white/90 font-medium">{auth.currentUser?.nickname || auth.currentUser?.username || "-"}</p>
              <p className="text-sm text-white/45">@{auth.currentUser?.username || "-"}</p>
            </div>

            <div className="rounded-[18px] border border-white/8 bg-white/[0.03] p-5 space-y-2">
              <p className="text-xs uppercase tracking-[0.22em] text-white/35">Runtime</p>
              <p className="text-sm text-white/70">connection: {connection.status}</p>
              <p className="text-sm text-white/70">inbox syncing: {connection.inboxSyncing ? "yes" : "no"}</p>
            </div>
          </div>

          <div className="mt-8 flex flex-wrap gap-3">
            <button
              className="rounded-full border border-white/10 px-4 py-2 text-sm text-white/85 hover:bg-white/5"
              onClick={() => void navigate({ to: "/contacts" })}
              type="button"
            >
              Go to Contacts
            </button>
            <button
              className="rounded-full border border-red-400/25 px-4 py-2 text-sm text-red-200 hover:bg-red-400/10"
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
