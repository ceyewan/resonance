import { useState } from "react";
import { useAuth } from "@/hooks/useAuth";
import { cn } from "@/lib/cn";

export default function LoginPage() {
  const [isLogin, setIsLogin] = useState(true);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const { login, register, isLoading, error } = useAuth();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!username || !password) return;

    try {
      if (isLogin) {
        await login(username, password);
      } else {
        await register(username, password);
      }
    } catch (err) {
      console.error(isLogin ? "Login failed:" : "Register failed:", err);
    }
  };

  return (
    <div className="flex h-full items-center justify-center bg-gradient-to-br from-primary to-primary/50">
      <div className="w-full max-w-md rounded-lg bg-card p-8 shadow-xl">
        <h1 className="mb-8 text-center text-3xl font-bold text-foreground">
          {isLogin ? "Resonance IM" : "创建账号"}
        </h1>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label
              htmlFor="username"
              className="block text-sm font-medium text-foreground"
            >
              用户名
            </label>
            <input
              id="username"
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="输入用户名"
              disabled={isLoading}
              className={cn(
                "mt-1 w-full rounded-md border border-input bg-background px-3 py-2",
                "text-foreground placeholder-muted-foreground",
                "focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/20",
                "disabled:opacity-50 disabled:cursor-not-allowed",
              )}
            />
          </div>

          <div>
            <label
              htmlFor="password"
              className="block text-sm font-medium text-foreground"
            >
              密码
            </label>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="输入密码"
              disabled={isLoading}
              className={cn(
                "mt-1 w-full rounded-md border border-input bg-background px-3 py-2",
                "text-foreground placeholder-muted-foreground",
                "focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/20",
                "disabled:opacity-50 disabled:cursor-not-allowed",
              )}
            />
          </div>

          {error && (
            <p className="rounded-md bg-destructive/10 p-2 text-sm text-destructive">
              {error}
            </p>
          )}

          <button
            type="submit"
            disabled={isLoading || !username || !password}
            className={cn(
              "w-full rounded-md bg-primary px-4 py-2 font-medium text-primary-foreground",
              "hover:bg-primary/90 focus:outline-none focus:ring-2 focus:ring-primary/50",
              "disabled:opacity-50 disabled:cursor-not-allowed transition-colors",
            )}
          >
            {isLoading
              ? isLogin
                ? "登录中..."
                : "注册中..."
              : isLogin
                ? "登录"
                : "注册"}
          </button>
        </form>

        <div className="mt-4 text-center text-sm text-muted-foreground">
          {isLogin ? "还没有账号？" : "已有账号？"}{" "}
          <button
            type="button"
            onClick={() => setIsLogin(!isLogin)}
            className="text-primary hover:underline focus:outline-none"
          >
            {isLogin ? "注册" : "登录"}
          </button>
        </div>
      </div>
    </div>
  );
}
