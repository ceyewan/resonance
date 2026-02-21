/**
 * Liquid Glass UI 组件套件
 *
 * 基于 Apple iOS 26 Liquid Glass 设计理念的 React 实现
 *
 * @example
 * ```tsx
 * // 1. 在应用根目录挂载滤镜
 * import { LiquidGlassFilter } from '@/components/ui/liquid-glass';
 *
 * export function App() {
 *   return (
 *     <>
 *       <LiquidGlassFilter />
 *       <YourAppContent />
 *     </>
 *   );
 * }
 *
 * // 2. 使用组件
 * import { LiquidGlassCard, LiquidButton } from '@/components/ui/liquid-glass';
 *
 * export function MyComponent() {
 *   return (
 *     <LiquidGlassCard intensity={2}>
 *       <h2>标题</h2>
 *       <LiquidButton variant="primary">操作</LiquidButton>
 *     </LiquidGlassCard>
 *   );
 * }
 * ```
 */

// 核心组件
export { LiquidGlassFilter } from './LiquidGlassFilter';
export { LiquidGlassCard } from './LiquidGlassCard';
export { LiquidButton } from './LiquidButton';

// 类型导出
export type { LiquidGlassFilterProps } from './LiquidGlassFilter';
export type { LiquidGlassCardProps } from './LiquidGlassCard';
export type { LiquidButtonProps } from './LiquidButton';
