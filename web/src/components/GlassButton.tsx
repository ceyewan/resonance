import { ReactNode } from "react";
import { useLiquidGlass } from "../hooks/useLiquidGlass";
import "./GlassButton.css";

interface GlassButtonProps {
  onClick?: (e: React.FormEvent) => void;
  children: ReactNode;
  type?: "button" | "submit" | "reset";
  className?: string;
  disabled?: boolean;
  /** 按钮风格变体 */
  variant?: "primary" | "ghost";
}

export function GlassButton({
  onClick,
  children,
  type = "button",
  className = "",
  disabled = false,
  variant = "primary",
}: GlassButtonProps) {
  const glass = useLiquidGlass<HTMLButtonElement>({
    damping: 0.2,
    stiffness: 0.12,
    tiltMax: 4,
    perspective: 800,
    activationRange: 120,
    glowIntensity: 1,
    pressScale: 0.94,
  });

  return (
    <button
      ref={glass.ref}
      type={type}
      onClick={onClick}
      disabled={disabled}
      {...(disabled ? {} : glass.handlers)}
      className={`glass-btn glass-btn--${variant} ${disabled ? "glass-btn--disabled" : ""} ${className}`}
    >
      {/* 折射微纹理层 */}
      <div className="glass-btn__refraction" />

      {/* 镜面高光追踪 */}
      <div className="glass-btn__specular" />

      {/* 内部柔光 */}
      <div className="glass-btn__inner-light" />

      {/* 按钮文字 */}
      <span className="glass-btn__label">{children}</span>
    </button>
  );
}
