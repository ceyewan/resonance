import { cn } from "@/lib/cn";

interface ConnectionStatusProps {
  isConnected: boolean;
  isConnecting?: boolean;
}

/**
 * 连接状态指示器组件
 * Liquid Glass T1 级实现
 */
export function ConnectionStatus({ isConnected, isConnecting }: ConnectionStatusProps) {
  const getStatusClass = () => {
    if (isConnected) return "connected";
    if (isConnecting) return "connecting";
    return "disconnected";
  };

  const getStatusText = () => {
    if (isConnecting) return "连接中...";
    if (isConnected) return "已连接";
    return "未连接";
  };

  return (
    <div className="lg-status-badge">
      <span className={cn("lg-status-dot", getStatusClass())} />
      <span className="text-xs text-slate-500 dark:text-slate-300">{getStatusText()}</span>
    </div>
  );
}
