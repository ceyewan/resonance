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
  const [nickname, setNickname] = useState("");
  const [validationErrors, setValidationErrors] = useState<{
    username?: string;
    password?: string;
    nickname?: string;
  }>({});

  const { login, register, isLoading, error, clearError } = useAuth();

  // 验证用户名（英文、数字、符号，3-20个字符）
  const validateUsername = useCallback((value: string): string | undefined => {
    if (!value) return "用户名不能为空";
    if (value.length < 3) return "用户名至少需要3个字符";
    if (value.length > 20) return "用户名不能超过20个字符";
    if (!/^[a-zA-Z0-9_\-@.]+$/.test(value)) {
      return "用户名只能包含英文字母、数字和符号(_-@.)";
    }
    return undefined;
  }, []);

  // 验证昵称（中文、英文、数字，1-20个字符）
  const validateNickname = useCallback((value: string): string | undefined => {
    if (!value) return "昵称不能为空";
    if (value.length > 20) return "昵称不能超过20个字符";
    return undefined;
  }, []);

  // 验证密码（至少6位，包含大小写、数字、符号）
  const validatePassword = useCallback((value: string): string | undefined => {
    if (!value) return "密码不能为空";
    if (value.length < 6) return "密码至少需要6个字符";
    if (value.length > 50) return "密码不能超过50个字符";
    // 至少包含大写、小写、数字、符号中的三种
    const hasUpperCase = /[A-Z]/.test(value);
    const hasLowerCase = /[a-z]/.test(value);
    const hasNumber = /[0-9]/.test(value);
    const hasSymbol = /[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(value);
    const types = [hasUpperCase, hasLowerCase, hasNumber, hasSymbol].filter(Boolean).length;
    if (types < 3) {
      return "密码需包含大写、小写、数字、符号中的至少三种";
    }
    return undefined;
  }, []);

  // 实时验证输入
  const handleUsernameChange = useCallback((value: string) => {
    setUsername(value);
    if (!isLogin) {
      setValidationErrors((prev) => ({ ...prev, username: validateUsername(value) }));
    }
    if (error) clearError();
  }, [isLogin, error, clearError, validateUsername]);

  const handleNicknameChange = useCallback((value: string) => {
    setNickname(value);
    setValidationErrors((prev) => ({ ...prev, nickname: validateNickname(value) }));
    if (error) clearError();
  }, [error, clearError, validateNickname]);

  const handlePasswordChange = useCallback((value: string) => {
    setPassword(value);
    setValidationErrors((prev) => ({ ...prev, password: validatePassword(value) }));
    if (error) clearError();
  }, [error, clearError, validatePassword]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    // 验证所有字段
    const errors: { username?: string; password?: string; nickname?: string } = {};
    errors.username = validateUsername(username);
    errors.password = validatePassword(password);
    if (!isLogin) {
      errors.nickname = validateNickname(nickname);
    }

    setValidationErrors(errors);

    // 检查是否有错误
    if (Object.values(errors).some(Boolean)) return;

    try {
      if (isLogin) {
        await login(username, password);
      } else {
        await register(username, password, nickname);
      }
    } catch (err) {
      console.error(isLogin ? "Login failed:" : "Register failed:", err);
    }
  };

  // 切换登录/注册模式时清除错误
  const handleToggleMode = useCallback(() => {
    setIsLogin((prev) => !prev);
    clearError();
    setValidationErrors({});
  }, [clearError]);

  // 检查是否可以提交
  const canSubmit = (() => {
    if (!username.trim() || !password.trim()) return false;
    if (!isLogin && !nickname.trim()) return false;
    if (Object.values(validationErrors).some(Boolean)) return false;
    return true;
  })();

  return (
    <div className="relative flex h-full items-center justify-center px-4 py-10">
      <div className="lg-glass-strong lg-animate-in w-full max-w-md rounded-3xl p-8 sm:p-9">
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
        <form onSubmit={handleSubmit} className="space-y-4">
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
              onChange={(e) => handleUsernameChange(e.target.value)}
              placeholder="英文、数字、符号（3-20位）"
              disabled={isLoading}
              autoComplete="username"
              className={cn(
                "lg-input w-full",
                validationErrors.username && "border-red-400 focus:border-red-500 focus:ring-red-500/20",
                "disabled:cursor-not-allowed disabled:opacity-50",
              )}
            />
            {validationErrors.username && (
              <p className="mt-1 text-xs text-red-500">{validationErrors.username}</p>
            )}
          </div>

          {/* 昵称输入（仅注册时显示） */}
          {!isLogin && (
            <div>
              <label
                htmlFor="nickname"
                className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-slate-200"
              >
                昵称
              </label>
              <input
                id="nickname"
                type="text"
                value={nickname}
                onChange={(e) => handleNicknameChange(e.target.value)}
                placeholder="中文、英文、数字（1-20位）"
                disabled={isLoading}
                autoComplete="nickname"
                className={cn(
                  "lg-input w-full",
                  validationErrors.nickname && "border-red-400 focus:border-red-500 focus:ring-red-500/20",
                  "disabled:cursor-not-allowed disabled:opacity-50",
                )}
              />
              {validationErrors.nickname && (
                <p className="mt-1 text-xs text-red-500">{validationErrors.nickname}</p>
              )}
            </div>
          )}

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
              onChange={(e) => handlePasswordChange(e.target.value)}
              placeholder="至少6位，含大小写、数字、符号"
              disabled={isLoading}
              autoComplete={isLogin ? "current-password" : "new-password"}
              className={cn(
                "lg-input w-full",
                validationErrors.password && "border-red-400 focus:border-red-500 focus:ring-red-500/20",
                "disabled:cursor-not-allowed disabled:opacity-50",
              )}
            />
            {validationErrors.password && (
              <p className="mt-1 text-xs text-red-500">{validationErrors.password}</p>
            )}
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
            disabled={isLoading || !canSubmit}
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
