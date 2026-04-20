import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { GlassCard } from "../../components/GlassCard";
import { GlassInput } from "../../components/GlassInput";
import { GlassButton } from "../../components/GlassButton";
import { WallpaperBackground } from "../../components/WallpaperBackground";
import { authClient } from "../../api/clients";
import { User, KeyRound, Type } from "lucide-react";
import "./AuthPages.css";

export function RegisterPage() {
  const [username, setUsername] = useState("");
  const [nickname, setNickname] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const navigate = useNavigate();

  const handleRegister = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!username.trim() || !password || !nickname.trim()) return;

    setLoading(true);
    setError("");

    try {
      await authClient.register({
        username,
        password,
        nickname,
      });
      // After successful registration, route to login to let them sign in.
      void navigate({ to: "/login" });
    } catch (cause: unknown) {
      setError(cause instanceof Error ? cause.message : "注册失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <WallpaperBackground>
      <div className="auth-page">
        <form onSubmit={(e) => void handleRegister(e)} className="auth-form">
          <GlassCard padding="44px 40px" cornerRadius={28}>
            {/* Header */}
            <div className="auth-header">
              <h1 className="auth-title">Create Account</h1>
              <p className="auth-subtitle">Join Resonance today</p>
            </div>

            {/* Input Fields */}
            <div className="auth-fields">
              <GlassInput
                autoComplete="username"
                icon={<User size={18} />}
                placeholder="Username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                disabled={loading}
              />

              <GlassInput
                icon={<Type size={18} />}
                placeholder="Nickname"
                value={nickname}
                onChange={(e) => setNickname(e.target.value)}
                disabled={loading}
              />

              <GlassInput
                autoComplete="new-password"
                icon={<KeyRound size={18} />}
                type="password"
                placeholder="Password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={loading}
              />
            </div>

            {/* Error */}
            {error && <p className="auth-error">{error}</p>}

            {/* Actions */}
            <div className="auth-actions">
              <GlassButton
                type="submit"
                disabled={loading || !username || !password || !nickname}
                onClick={(e) => void handleRegister(e)}
              >
                {loading ? "Signing up..." : "Sign Up"}
              </GlassButton>

              <p className="auth-switch">
                Already have an account?{" "}
                <Link to="/login" className="auth-switch__link">
                  Sign In
                </Link>
              </p>
            </div>
          </GlassCard>
        </form>
      </div>
    </WallpaperBackground>
  );
}
