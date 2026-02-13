import { useState, useCallback } from "react";
import { useAuth } from "@/hooks/useAuth";
import { cn } from "@/lib/cn";

/**
 * 登录/注册页面
 * Telegram 风格的简洁设计
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
    <div className="flex h-full items-center justify-center bg-gradient-to-br from-sky-500 to-blue-600">
      <div className="w-full max-w-md rounded-2xl bg-white p-8 shadow-2xl dark:bg-gray-800">
        {/* Logo / 标题 */}
        <div className="mb-8 text-center">
          <div className="mb-4 flex justify-center">
            {/* 简单的纸飞机图标 */}
            <svg
              className="h-16 w-16 text-sky-500"
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
          <h1 className="text-3xl font-bold text-gray-900 dark:text-white">Resonance</h1>
          <p className="mt-2 text-sm text-gray-600 dark:text-gray-400">
            {isLogin ? "登录到您的账号" : "创建新账号"}
          </p>
        </div>

        {/* 表单 */}
        <form onSubmit={handleSubmit} className="space-y-5">
          {/* 用户名输入 */}
          <div>
            <label
              htmlFor="username"
              className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300"
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
                "w-full rounded-lg border border-gray-300 px-4 py-2.5",
                "text-gray-900 placeholder-gray-400",
                "bg-gray-50 focus:border-sky-500 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20",
                "disabled:opacity-50 disabled:cursor-not-allowed",
                "dark:border-gray-600 dark:bg-gray-700 dark:text-white dark:placeholder-gray-500",
                "dark:focus:border-sky-400 dark:focus:bg-gray-800",
                "transition-colors",
              )}
            />
          </div>

          {/* 密码输入 */}
          <div>
            <label
              htmlFor="password"
              className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300"
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
                "w-full rounded-lg border border-gray-300 px-4 py-2.5",
                "text-gray-900 placeholder-gray-400",
                "bg-gray-50 focus:border-sky-500 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20",
                "disabled:opacity-50 disabled:cursor-not-allowed",
                "dark:border-gray-600 dark:bg-gray-700 dark:text-white dark:placeholder-gray-500",
                "dark:focus:border-sky-400 dark:focus:bg-gray-800",
                "transition-colors",
              )}
            />
          </div>

          {/* 错误提示 */}
          {error && (
            <div className="flex items-center gap-2 rounded-lg bg-red-50 p-3 text-red-600 dark:bg-red-900/20 dark:text-red-400">
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
              "w-full rounded-lg bg-sky-500 px-4 py-3 font-semibold text-white",
              "hover:bg-sky-600 focus:outline-none focus:ring-2 focus:ring-sky-500/50 focus:ring-offset-2",
              "disabled:opacity-50 disabled:cursor-not-allowed",
              "transition-colors",
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
        <div className="mt-6 text-center text-sm text-gray-600 dark:text-gray-400">
          {isLogin ? "还没有账号？ " : "已有账号？ "}
          <button
            type="button"
            onClick={handleToggleMode}
            disabled={isLoading}
            className="font-semibold text-sky-500 hover:text-sky-600 focus:outline-none focus:underline disabled:opacity-50 disabled:cursor-not-allowed dark:text-sky-400 dark:hover:text-sky-300"
          >
            {isLogin ? "立即注册" : "返回登录"}
          </button>
        </div>

        {/* 底部说明 */}
        <p className="mt-8 text-center text-xs text-gray-500 dark:text-gray-500">
          登录即表示您同意我们的服务条款和隐私政策
        </p>
      </div>
    </div>
  );
}
