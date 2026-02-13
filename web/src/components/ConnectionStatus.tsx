import { cn } from "@/lib/cn";

interface ConnectionStatusProps {
  isConnected: boolean;
  isConnecting?: boolean;
}

/**
 * 连接状态指示器组件
 *
 * 显示 WebSocket 连接状态，带颜色指示点
 */
export function ConnectionStatus({ isConnected, isConnecting }: ConnectionStatusProps) {
  return (
    <div className="flex items-center gap-1.5 rounded-full border border-white/45 bg-white/45 px-2.5 py-1 backdrop-blur-md dark:border-slate-200/10 dark:bg-slate-800/45">
      <span
        className={cn(
          "h-2 w-2 rounded-full",
          isConnected
            ? "bg-green-500 shadow-[0_0_10px_rgba(34,197,94,0.75)]"
            : isConnecting
              ? "bg-yellow-500 animate-pulse shadow-[0_0_10px_rgba(234,179,8,0.75)]"
              : "bg-red-500 shadow-[0_0_10px_rgba(239,68,68,0.75)]",
        )}
      />
      <span className="text-xs text-slate-500 dark:text-slate-300">
        {isConnecting ? "连接中..." : isConnected ? "已连接" : "未连接"}
      </span>
    </div>
  );
}
