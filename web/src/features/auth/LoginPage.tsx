import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { GlassCard } from "../../components/GlassCard";
import { GlassInput } from "../../components/GlassInput";
import { GlassButton } from "../../components/GlassButton";
import { WallpaperBackground } from "../../components/WallpaperBackground";
import { login } from "../../services/auth";
import { KeyRound, User } from "lucide-react";
import "./AuthPages.css";

export function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const navigate = useNavigate();

  const handleLogin = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!username.trim() || !password) return;

    setLoading(true);
    setError("");

    try {
      await login(username, password);
      void navigate({ to: "/chat" });
    } catch (cause: unknown) {
      setError(cause instanceof Error ? cause.message : "登录失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <WallpaperBackground>
      <div className="auth-page">
        <form onSubmit={(e) => void handleLogin(e)} className="auth-form">
          <GlassCard padding="44px 40px" cornerRadius={28}>
            {/* Brand Header */}
            <div className="auth-header">
              <div className="auth-logo">
                <div className="auth-logo__icon">
                  <span className="auth-logo__letter">R</span>
                </div>
                <div className="auth-logo__ripple" />
              </div>
              <h1 className="auth-title">Resonance</h1>
              <p className="auth-subtitle">Welcome back</p>
            </div>

            {/* Input Fields */}
            <div className="auth-fields">
              <GlassInput
                autoComplete="username"
                icon={<User size={18} />}
                placeholder="Email or Username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                disabled={loading}
              />

              <GlassInput
                autoComplete="current-password"
                icon={<KeyRound size={18} />}
                type="password"
                placeholder="Password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={loading}
              />
            </div>

            {/* Error Message */}
            {error && <p className="auth-error">{error}</p>}

            {/* Actions */}
            <div className="auth-actions">
              <GlassButton
                type="submit"
                disabled={loading || !username || !password}
                onClick={(e) => void handleLogin(e)}
              >
                {loading ? "Signing in..." : "Sign In"}
              </GlassButton>

              <p className="auth-switch">
                Don't have an account?{" "}
                <Link to="/register" className="auth-switch__link">
                  Sign Up
                </Link>
              </p>
            </div>
          </GlassCard>
        </form>
      </div>
    </WallpaperBackground>
  );
}
