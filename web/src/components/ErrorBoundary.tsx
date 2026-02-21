import { Component, ReactNode } from "react";

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback?: ReactNode;
  onError?: (error: Error, errorInfo: React.ErrorInfo) => void;
}

interface ErrorBoundaryState {
  hasError: boolean;
  error?: Error;
}

/**
 * 错误边界组件
 *
 * 捕获子组件树中的 JavaScript 错误，记录错误日志，并显示备用 UI
 *
 * 用法：
 * ```tsx
 * <ErrorBoundary fallback={<ErrorFallback />}>
 *   <YourComponent />
 * </ErrorBoundary>
 * ```
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo): void {
    // 记录错误到控制台
    console.error("[ErrorBoundary] Caught error:", error, errorInfo);

    // 调用自定义错误处理器
    this.props.onError?.(error, errorInfo);
  }

  render(): ReactNode {
    if (this.state.hasError) {
      // 使用自定义备用 UI 或默认备用 UI
      return (
        this.props.fallback || (
          <div className="flex h-screen w-screen items-center justify-center bg-gradient-to-br from-sky-100/60 via-blue-50/40 to-white dark:from-slate-900 dark:via-slate-800 dark:to-slate-900">
            <div className="lg-glass-strong rounded-3xl p-8 text-center">
              <div className="mb-4 text-6xl">⚠️</div>
              <h1 className="mb-2 text-xl font-semibold text-slate-900 dark:text-white">出错了</h1>
              <p className="mb-4 text-sm text-slate-600 dark:text-slate-400">
                应用遇到了意外错误，请刷新页面重试
              </p>
              <button
                onClick={() => window.location.reload()}
                className="lg-btn-primary"
              >
                刷新页面
              </button>
              {process.env.NODE_ENV === "development" && this.state.error && (
                <details className="mt-4 text-left">
                  <summary className="cursor-pointer text-sm text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-300">
                    错误详情
                  </summary>
                  <pre className="lg-glass mt-2 overflow-auto rounded-xl p-4 text-xs text-red-600 dark:text-red-400">
                    {this.state.error.stack}
                  </pre>
                </details>
              )}
            </div>
          </div>
        )
      );
    }

    return this.props.children;
  }
}
