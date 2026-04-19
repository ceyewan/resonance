import { forwardRef, ReactNode } from "react";
import { useLiquidGlass } from "../hooks/useLiquidGlass";
import "./GlassCard.css";

export interface GlassCardProps {
  children: ReactNode;
  className?: string;
  mouseContainer?: React.RefObject<HTMLElement | null>; // 向后兼容
  padding?: string;
  cornerRadius?: number;
  /** 是否启用 3D 倾斜效果 */
  enableTilt?: boolean;
  /** 是否启用折射滤镜 */
  enableRefraction?: boolean;
}

export const GlassCard = forwardRef<HTMLDivElement, GlassCardProps>(
  (
    {
      children,
      className = "",
      padding = "40px",
      cornerRadius = 28,
      enableTilt = true,
      enableRefraction = true,
    },
    ref
  ) => {
    const glass = useLiquidGlass<HTMLDivElement>({
      tiltMax: enableTilt ? 6 : 0,
      damping: 0.12,
      stiffness: 0.06,
      glowIntensity: 0.9,
      enableRefraction,
      pressScale: 0.985,
    });

    const setRefs = (element: HTMLDivElement) => {
      // @ts-expect-error 更新内部 ref
      glass.ref.current = element;
      if (typeof ref === "function") {
        ref(element);
      } else if (ref) {
        (ref as React.MutableRefObject<HTMLDivElement | null>).current =
          element;
      }
    };

    return (
      <div
        ref={setRefs}
        {...glass.handlers}
        className={`glass-card ${className}`}
        style={{
          borderRadius: cornerRadius,
          padding,
        }}
      >
        {/* Layer 1: 折射背景层 —— 通过 backdrop-filter 弯曲下层内容 */}
        <div className="glass-card__refraction" />

        {/* Layer 2: 环境反光层 —— 模拟天光从上方洒下，底部渐暗 */}
        <div className="glass-card__ambient" />

        {/* Layer 3: 镜面高光层 —— 跟随鼠标的动态光斑 */}
        <div className="glass-card__specular" />

        {/* Layer 4: 边缘高光 —— 玻璃边缘的"脊线光" */}
        <div className="glass-card__rim" />

        {/* Content Layer */}
        <div className="glass-card__content">
          {children}
        </div>
      </div>
    );
  }
);
GlassCard.displayName = "GlassCard";
