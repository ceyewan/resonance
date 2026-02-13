import { useState, useCallback } from "react";
import { useAuth } from "@/hooks/useAuth";
import { cn } from "@/lib/cn";

/**
 * 登录/注册页面
 * Liquid Glass 设计风格
 */
export default function LoginPage() {
  const [isLogin, setIsLogin] = useState(true);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const { login, register, isLoading, error, clearError } = useAuth();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!username.trim() || !password.trim()) return;

    try {
      if (isLogin) {
        await login(username, password);
      } else {
        await register(username, password);
      }
    } catch (err) {
      // 错误已经在 useAuth 中处理
      console.error(isLogin ? "Login failed:" : "Register failed:", err);
    }
  };

  // 切换登录/注册模式时清除错误
  const handleToggleMode = useCallback(() => {
    setIsLogin((prev) => !prev);
    clearError();
  }, [clearError]);

  // 输入时清除错误
  const handleInputChange = useCallback(() => {
    if (error) clearError();
  }, [error, clearError]);

  return (
    <div className="relative flex h-full items-center justify-center px-4 py-10">
      <div className="lg-glass-3 lg-glow-border lg-glow-top lg-animate-in w-full max-w-md rounded-3xl p-8 sm:p-9">
        {/* Logo / 标题 */}
        <div className="mb-8 text-center">
          <div className="mb-4 flex justify-center">
            {/* 简单的纸飞机图标 */}
            <svg
              className="h-16 w-16 text-sky-500 drop-shadow-[0_12px_22px_rgba(2,132,199,0.45)]"
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
          </div>
          <h1 className="text-3xl font-bold text-slate-900 dark:text-white">Resonance</h1>
          <p className="mt-2 text-sm text-slate-600 dark:text-slate-300">
            {isLogin ? "登录到您的账号" : "创建新账号"}
          </p>
        </div>

        {/* 表单 */}
        <form onSubmit={handleSubmit} className="space-y-5">
          {/* 用户名输入 */}
          <div>
            <label
              htmlFor="username"
              className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-slate-200"
            >
              用户名
            </label>
            <input
              id="username"
              type="text"
              value={username}
              onChange={(e) => {
                setUsername(e.target.value);
                handleInputChange();
              }}
              placeholder="请输入用户名"
              disabled={isLoading}
              autoComplete="username"
              className={cn(
                "lg-input w-full",
                "disabled:cursor-not-allowed disabled:opacity-50",
              )}
            />
          </div>

          {/* 密码输入 */}
          <div>
            <label
              htmlFor="password"
              className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-slate-200"
            >
              密码
            </label>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
                handleInputChange();
              }}
              placeholder="请输入密码"
              disabled={isLoading}
              autoComplete={isLogin ? "current-password" : "new-password"}
              className={cn(
                "lg-input w-full",
                "disabled:cursor-not-allowed disabled:opacity-50",
              )}
            />
          </div>

          {/* 错误提示 */}
          {error && (
            <div className="flex items-center gap-2 rounded-xl border border-red-200/60 bg-red-50/85 p-3 text-red-600 backdrop-blur-sm dark:border-red-300/20 dark:bg-red-950/35 dark:text-red-300">
              <svg className="h-5 w-5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
                <path
                  fillRule="evenodd"
                  d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z"
                  clipRule="evenodd"
                />
              </svg>
              <span className="text-sm">{error}</span>
            </div>
          )}

          {/* 提交按钮 */}
          <button
            type="submit"
            disabled={isLoading || !username.trim() || !password.trim()}
            className={cn(
              "lg-btn-primary w-full",
              "focus:outline-none focus:ring-2 focus:ring-sky-400/40 focus:ring-offset-2 focus:ring-offset-transparent",
              "disabled:cursor-not-allowed disabled:opacity-55",
            )}
          >
            {isLoading ? (
              <span className="flex items-center justify-center gap-2">
                <svg className="h-4 w-4 animate-spin" fill="none" viewBox="0 0 24 24">
                  <circle
                    className="opacity-25"
                    cx="12"
                    cy="12"
                    r="10"
                    stroke="currentColor"
                    strokeWidth="4"
                  />
                  <path
                    className="opacity-75"
                    fill="currentColor"
                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                  />
                </svg>
                {isLogin ? "登录中..." : "注册中..."}
              </span>
            ) : isLogin ? (
              "登录"
            ) : (
              "注册"
            )}
          </button>
        </form>

        {/* 切换登录/注册 */}
        <div className="mt-6 text-center text-sm text-slate-600 dark:text-slate-300">
          {isLogin ? "还没有账号？ " : "已有账号？ "}
          <button
            type="button"
            onClick={handleToggleMode}
            disabled={isLoading}
            className="font-semibold text-sky-600 hover:text-sky-700 focus:outline-none focus:underline disabled:cursor-not-allowed disabled:opacity-50 dark:text-sky-300 dark:hover:text-sky-200"
          >
            {isLogin ? "立即注册" : "返回登录"}
          </button>
        </div>

        {/* 底部说明 */}
        <p className="mt-8 text-center text-xs text-slate-500 dark:text-slate-400">
          登录即表示您同意我们的服务条款和隐私政策
        </p>
      </div>
    </div>
  );
}
