import { useAuthStore } from "@/stores/auth";
import { ConnectionStatus } from "@/components/ConnectionStatus";

interface ChatHeaderProps {
  isConnected: boolean;
  isConnecting?: boolean;
}

/**
 * 聊天页面顶部导航栏
 */
export function ChatHeader({ isConnected, isConnecting }: ChatHeaderProps) {
  const { user, logout } = useAuthStore();

  return (
    <header className="lg-glass-strong sticky top-0 z-40 mb-3 flex h-14 shrink-0 items-center justify-between rounded-2xl px-4">
      <div className="flex items-center gap-3">
        {/* Logo */}
        <svg
          className="h-6 w-6 text-sky-500 drop-shadow-[0_10px_18px_rgba(2,132,199,0.42)]"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={2}
            d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8"
          />
        </svg>
        <h1 className="text-lg font-semibold text-gray-900 dark:text-white">Resonance</h1>
        <ConnectionStatus isConnected={isConnected} isConnecting={isConnecting} />
      </div>

      <div className="flex items-center gap-3">
        {/* 用户信息 */}
        <span className="text-sm text-slate-600 dark:text-slate-300">
          {user?.nickname || user?.username}
        </span>

        {/* 登出按钮 */}
        <button
          onClick={logout}
          className="rounded-xl px-3 py-1.5 text-sm font-medium text-slate-600 transition-all duration-200 hover:bg-white/45 hover:text-slate-900 hover:shadow-md dark:text-slate-300 dark:hover:bg-slate-700/55 dark:hover:text-white"
        >
          登出
        </button>
      </div>
    </header>
  );
}
