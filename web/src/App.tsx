import { FormEvent, useState } from "react";
import { authClient } from "./api/clients";
import { setAccessToken } from "./api/transport";

export default function App() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState("");
  const [error, setError] = useState("");

  const onSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setLoading(true);
    setError("");
    setResult("");

    try {
      const response = await authClient.login({
        username: username.trim(),
        password,
      });

      if (response.accessToken) {
        setAccessToken(response.accessToken);
      }

      const name = response.user?.nickname || response.user?.username || "-";
      setResult(
        `登录成功: user=${name}, token=${response.accessToken ? "已写入 localStorage" : "空"}`,
      );
    } catch (cause: unknown) {
      setError(
        `登录失败: ${cause instanceof Error ? cause.message : "未知错误"}`,
      );
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="mx-auto flex min-h-screen w-full max-w-xl items-center px-6">
      <section className="w-full rounded-xl border border-(--color-border) p-6 shadow-sm">
        <h1 className="text-xl font-semibold">S1 Login Demo</h1>
        <p className="mt-1 text-sm text-(--color-text-muted)">
          该页面直接调用 AuthService.Login 验证 ConnectRPC 底座。
        </p>

        <form
          className="mt-5 space-y-4"
          onSubmit={(event) => void onSubmit(event)}
        >
          <label className="block">
            <span className="mb-1 block text-sm">Username</span>
            <input
              autoComplete="username"
              className="w-full rounded-md border border-(--color-border) px-3 py-2 outline-none focus:ring-2 focus:ring-(--color-primary)"
              disabled={loading}
              onChange={(event) => setUsername(event.target.value)}
              placeholder="alice"
              value={username}
            />
          </label>

          <label className="block">
            <span className="mb-1 block text-sm">Password</span>
            <input
              autoComplete="current-password"
              className="w-full rounded-md border border-(--color-border) px-3 py-2 outline-none focus:ring-2 focus:ring-(--color-primary)"
              disabled={loading}
              onChange={(event) => setPassword(event.target.value)}
              placeholder="******"
              type="password"
              value={password}
            />
          </label>

          <button
            className="rounded-md bg-(--color-primary) px-4 py-2 text-sm font-medium text-white disabled:opacity-60"
            disabled={loading || !username.trim() || !password}
            type="submit"
          >
            {loading ? "登录中..." : "调用 AuthService.Login"}
          </button>
        </form>

        {result ? (
          <p className="mt-4 text-sm text-green-600">{result}</p>
        ) : null}
        {error ? <p className="mt-4 text-sm text-red-600">{error}</p> : null}
      </section>
    </main>
  );
}
