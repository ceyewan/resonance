import { Outlet } from "@tanstack/react-router";

import { WallpaperBackground } from "../../components/WallpaperBackground";
import { useAuthGuard } from "../../hooks/useAuthGuard";
import { SessionRail } from "./SessionRail";

export function ChatLayout() {
  const auth = useAuthGuard();

  if (auth.bootstrapping) {
    return (
      <WallpaperBackground>
        <div className="flex h-screen w-full items-center justify-center">
          <div className="text-white/50 text-sm">Loading...</div>
        </div>
      </WallpaperBackground>
    );
  }

  return (
    <WallpaperBackground>
      <div className="flex h-screen w-full max-w-[1680px] mx-auto p-4 md:p-6 gap-4 md:gap-6">
        <aside className="w-full max-w-[340px] h-full flex-shrink-0 hidden md:block">
          <SessionRail />
        </aside>

        <main className="flex-1 h-full min-w-0">
          <Outlet />
        </main>
      </div>
    </WallpaperBackground>
  );
}
