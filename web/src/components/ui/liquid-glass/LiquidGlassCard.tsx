import { forwardRef, type ReactNode } from 'react';
import { cn } from '@/lib/cn';

export interface LiquidGlassCardProps extends React.HTMLAttributes<HTMLDivElement> {
  /**
   * 卡片内容
   */
  children: ReactNode;

  /**
   * 内容稳定板的色调
   * - light: 亮色遮罩（适合深色背景）
   * - dark: 暗色遮罩（适合浅色背景）
   * - none: 无遮罩（需自行确保对比度）
   * @default 'light'
   */
  scrimTint?: 'light' | 'dark' | 'none';

  /**
   * 是否启用 SVG 折射滤镜
   * 注意：需要先在应用根目录挂载 LiquidGlassFilter 组件
   * @default false
   */
  useRefraction?: boolean;

  /**
   * 玻璃强度等级
   * - 1: 轻量（列表项、小卡片）
   * - 2: 中等（面板、弹窗）
   * - 3: 强（Modal、重要 CTA）
   * @default 1
   */
  intensity?: 1 | 2 | 3;

  /**
   * 是否启用悬停效果
   * @default true
   */
  hoverable?: boolean;

  /**
   * 内边距
   * @default 'p-6'
   */
  padding?: string;
}

/**
 * Liquid Glass 卡片组件
 *
 * 遵循双层堆叠模型：
 * - 底层：折射背景层（Z: 0）
 * - 中层：边缘高光层（Z: 10）
 * - 上层：内容稳定板（Z: 20）
 *
 * @example
 * ```tsx
 * import { LiquidGlassCard } from '@/components/ui/liquid-glass';
 *
 * export function Example() {
 *   return (
 *     <LiquidGlassCard intensity={2} hoverable>
 *       <h2>标题</h2>
 *       <p>内容</p>
 *     </LiquidGlassCard>
 *   );
 * }
 * ```
 */
export const LiquidGlassCard = forwardRef<HTMLDivElement, LiquidGlassCardProps>(
  (
    {
      children,
      className,
      scrimTint = 'light',
      useRefraction = false,
      intensity = 1,
      hoverable = true,
      padding = 'p-6',
      ...props
    },
    ref
  ) => {
    // 根据强度选择模糊半径
    const blurRadius = intensity === 1 ? 8 : intensity === 2 ? 16 : 24;

    // 内容稳定板样式
    const scrimClass = {
      light: 'bg-white/10 border-white/20',
      dark: 'bg-black/30 border-white/10',
      none: 'border-transparent',
    }[scrimTint];

    return (
      <div
        ref={ref}
        className={cn(
          'relative overflow-hidden rounded-2xl shadow-2xl group',
          hoverable && 'cursor-pointer',
          className
        )}
        {...props}
      >
        {/* === 底层：折射背景层 (Z: 0) === */}
        <div
          className="absolute inset-0 z-0 transition-all duration-500 ease-out"
          style={{
            backdropFilter: useRefraction
              ? `url(#liquid-glass-refraction) blur(${blurRadius}px) brightness(115%) saturate(120%)`
              : `blur(${blurRadius * 2}px) saturate(120%)`,
            WebkitBackdropFilter: useRefraction
              ? `url(#liquid-glass-refraction) blur(${blurRadius}px) brightness(115%) saturate(120%)`
              : `blur(${blurRadius * 2}px) saturate(120%)`,
          }}
          aria-hidden="true"
        />

        {/* === 中层：物理边界与高光 (Z: 10) === */}
        <div
          className={cn(
            'absolute inset-0 border pointer-events-none z-10',
            'shadow-[inset_0_1px_1px_rgba(255,255,255,0.6),inset_0_-1px_1px_rgba(255,255,255,0.1)]',
            scrimClass
          )}
        />

        {/* === 上层：绝对清晰的内容容器 (Z: 20) === */}
        <div className={cn('relative z-20 w-full antialiased', padding)}>
          {children}
        </div>

        {/* 悬停效果（仅视觉） */}
        {hoverable && (
          <style>{`
            .group:hover .z-0 {
              transform: scale(1.02);
            }
          `}</style>
        )}
      </div>
    );
  }
);

LiquidGlassCard.displayName = 'LiquidGlassCard';
