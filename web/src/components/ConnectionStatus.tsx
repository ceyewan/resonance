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
    <div className="flex items-center gap-1.5">
      <span
        className={cn(
          "h-2 w-2 rounded-full",
          isConnected
            ? "bg-green-500"
            : isConnecting
              ? "bg-yellow-500 animate-pulse"
              : "bg-red-500",
        )}
      />
      <span className="text-xs text-gray-500 dark:text-gray-400">
        {isConnecting ? "连接中..." : isConnected ? "已连接" : "未连接"}
      </span>
    </div>
  );
}
