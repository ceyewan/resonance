import { memo } from 'react';

export interface LiquidGlassFilterProps {
  /**
   * SVG 滤镜 ID，用于 CSS url() 引用
   * @default 'liquid-glass-refraction'
   */
  id?: string;

  /**
   * 折射强度（像素偏移量）
   * @default 45
   */
  refractionScale?: number;

  /**
   * 基础模糊半径
   * 注意：过大的值会严重影响性能
   * @default 3
   */
  baseBlurRadius?: number;

  /**
   * 色彩饱和度补偿系数
   * @default 1.6
   */
  saturationBoost?: number;

  /**
   * 噪声基础频率（控制折射纹理密度）
   * @default 0.04
   */
  noiseFrequency?: number;

  /**
   * 噪声种子（用于保持纹理一致性）
   * @default 42
   */
  noiseSeed?: number;
}

/**
 * Liquid Glass SVG 折射滤镜组件
 *
 * 使用方法：
 * 1. 在应用根目录（App.tsx）挂载此组件一次
 * 2. 在 CSS 中通过 backdrop-filter: url(#liquid-glass-refraction) 引用
 *
 * @example
 * ```tsx
 * // App.tsx
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
 * ```
 *
 * @example
 * ```css
 * /* 在 CSS 或 Tailwind 类中 *\/
 * .glass-with-refraction {
 *   backdrop-filter: url(#liquid-glass-refraction) blur(8px);
 * }
 * ```
 */
export const LiquidGlassFilter = memo<LiquidGlassFilterProps>(({
  id = 'liquid-glass-refraction',
  refractionScale = 45,
  baseBlurRadius = 3,
  saturationBoost = 1.6,
  noiseFrequency = 0.04,
  noiseSeed = 42,
}) => {
  return (
    <svg
      style={{
        position: 'absolute',
        width: 0,
        height: 0,
        pointerEvents: 'none',
      }}
      aria-hidden="true"
    >
      <defs>
        <filter
          id={id}
          x="-30%"
          y="-30%"
          width="160%"
          height="160%"
          colorInterpolationFilters="sRGB"
        >
          {/* Step 1: 基础光学散射 */}
          <feGaussianBlur
            in="SourceGraphic"
            stdDeviation={baseBlurRadius}
            result="base_blur"
          />

          {/* Step 2: 程序化位移贴图（使用 Perlin 噪声） */}
          <feTurbulence
            type="fractalNoise"
            baseFrequency={noiseFrequency}
            numOctaves="3"
            seed={noiseSeed}
            result="noise"
          />

          {/* Step 3: 核心折射算法
              P'(x,y) = P(x + scale*(R-0.5), y + scale*(G-0.5))
              其中 R, G 为噪声图的红绿通道值 [0,1]
          */}
          <feDisplacementMap
            in="base_blur"
            in2="noise"
            scale={refractionScale}
            xChannelSelector="R"
            yChannelSelector="G"
            result="refracted_layer"
          />

          {/* Step 4: 光学补偿（消除模糊导致的发灰现象） */}
          <feColorMatrix
            in="refracted_layer"
            type="saturate"
            values={saturationBoost.toString()}
            result="compensated_layer"
          />

          {/* Step 5: 输出限制（裁剪到元素形状内） */}
          <feComposite
            in="compensated_layer"
            in2="SourceGraphic"
            operator="in"
          />
        </filter>
      </defs>
    </svg>
  );
});

LiquidGlassFilter.displayName = 'LiquidGlassFilter';
