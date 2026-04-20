import { ReactNode } from "react";
import "./WallpaperBackground.css";

interface WallpaperBackgroundProps {
  children?: ReactNode;
}

/**
 * 液态玻璃壁纸背景
 *
 * 多层架构：
 *   1. 动态渐变壁纸（缓慢流动的色彩）
 *   2. 装饰光球（漂浮的发光体增加空间感）
 *   3. 噪点纹理覆盖（微妙的颗粒感增加真实性）
 *   4. 微光粒子（极微小的闪烁点）
 */
export function WallpaperBackground({ children }: WallpaperBackgroundProps) {
  return (
    <div className="wallpaper">
      {/* Layer 1: 流动渐变壁纸 */}
      <div className="wallpaper__gradient" />

      {/* Layer 2: 装饰光球 */}
      <div className="wallpaper__orb wallpaper__orb--primary" />
      <div className="wallpaper__orb wallpaper__orb--secondary" />
      <div className="wallpaper__orb wallpaper__orb--accent" />

      {/* Layer 3: 噪点纹理覆盖（CSS 生成，无需外部图片） */}
      <div className="wallpaper__noise" />

      {/* Layer 4: 微光粒子 */}
      <div className="wallpaper__shimmer" />

      {/* Content */}
      <div className="wallpaper__content">{children}</div>
    </div>
  );
}
