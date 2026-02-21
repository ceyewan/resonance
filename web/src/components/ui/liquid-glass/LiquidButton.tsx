import { forwardRef, type ReactNode } from 'react';
import { cn } from '@/lib/cn';

export interface LiquidButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  /**
   * 按钮变体
   * - primary: 主要操作（渐变背景 + 毛玻璃）
   * - secondary: 次要操作（透明玻璃）
   * - glass: 纯玻璃风格
   * @default 'primary'
   */
  variant?: 'primary' | 'secondary' | 'glass';

  /**
   * 按钮尺寸
   * @default 'md'
   */
  size?: 'sm' | 'md' | 'lg';

  /**
   * 子元素
   */
  children: ReactNode;
}

const sizeClasses = {
  sm: 'px-4 py-1.5 text-sm rounded-xl',
  md: 'px-5 py-2.5 text-base rounded-2xl',
  lg: 'px-6 py-3 text-lg rounded-2xl',
};

/**
 * Liquid Glass 按钮组件
 *
 * 提供物理反馈的交互式按钮：
 * - Hover: 浮起 + 高光增强
 * - Active: 下压 + 阴影收缩
 * - Focus: 清晰外环（无障碍）
 *
 * @example
 * ```tsx
 * import { LiquidButton } from '@/components/ui/liquid-glass';
 *
 * export function Example() {
 *   return (
 *     <div className="flex gap-3">
 *       <LiquidButton variant="primary">发送</LiquidButton>
 *       <LiquidButton variant="secondary">取消</LiquidButton>
 *       <LiquidButton variant="glass">了解更多</LiquidButton>
 *     </div>
 *   );
 * }
 * ```
 */
export const LiquidButton = forwardRef<HTMLButtonElement, LiquidButtonProps>(
  ({ className, variant = 'primary', size = 'md', children, disabled, ...props }, ref) => {
    const baseClasses = cn(
      'inline-flex items-center justify-center gap-2 font-semibold',
      'transition-all duration-200 ease-out',
      'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-sky-500/50 focus-visible:ring-offset-2',
      'disabled:cursor-not-allowed disabled:opacity-50',
      sizeClasses[size]
    );

    const variantClasses = {
      primary: cn(
        'text-white',
        'bg-gradient-to-br from-sky-500/95 to-sky-600/98',
        'backdrop-blur-md',
        'border border-white/10',
        'shadow-[0_16px_32px_-20px_rgb(2_132_199_/0.7),inset_0_1px_0_rgba(255,255,255,0.3)]',
        'hover:shadow-[0_20px_40px_-20px_rgb(2_132_199_/0.85),inset_0_1px_0_rgba(255,255,255,0.35)]',
        'hover:-translate-y-0.5 hover:scale-[1.01]',
        'active:shadow-[0_8px_16px_-12px_rgb(2_132_199_/0.6),inset_0_1px_0_rgba(255,255,255,0.2)]',
        'active:translate-y-0 active:scale-0.98'
      ),
      secondary: cn(
        'text-slate-900 dark:text-slate-100',
        'bg-white/55 dark:bg-slate-800/55',
        'backdrop-blur-lg',
        'border border-white/40 dark:border-slate-600/30',
        'shadow-[0_12px_24px_-18px_rgba(15,23,42,0.3),inset_0_1px_0_rgba(255,255,255,0.7)]',
        'dark:shadow-[0_12px_24px_-18px_rgba(0,0,0,0.5),inset_0_1px_0_rgba(148,163,184,0.15)]',
        'hover:bg-white/65 dark:hover:bg-slate-800/65',
        'hover:-translate-y-0.5',
        'active:translate-y-0'
      ),
      glass: cn(
        'text-slate-900 dark:text-slate-100',
        'lg-glass-1',
        'hover:-translate-y-0.5 hover:scale-105',
        'active:translate-y-0 active:scale-95'
      ),
    };

    return (
      <button
        ref={ref}
        className={cn(baseClasses, variantClasses[variant], className)}
        disabled={disabled}
        {...props}
      >
        {children}
      </button>
    );
  }
);

LiquidButton.displayName = 'LiquidButton';
