import { forwardRef, InputHTMLAttributes } from "react";
import "./GlassInput.css";

interface GlassInputProps extends InputHTMLAttributes<HTMLInputElement> {
  icon?: React.ReactNode;
}

export const GlassInput = forwardRef<HTMLInputElement, GlassInputProps>(
  ({ className = "", icon, ...props }, ref) => {
    return (
      <div className={`glass-input-wrapper ${className}`}>
        <input
          ref={ref}
          className="glass-input"
          {...props}
          style={{
            paddingLeft: icon ? "3rem" : "1.25rem",
            ...props.style,
          }}
        />

        {/* 焦点时的发光脊线 */}
        <div className="glass-input__focus-glow" />

        {/* 微妙的内部天光 */}
        <div className="glass-input__ambient" />

        {icon && <div className="glass-input__icon">{icon}</div>}
      </div>
    );
  },
);
GlassInput.displayName = "GlassInput";
