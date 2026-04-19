/**
 * 全局 SVG 滤镜池 —— Liquid Glass Shader Pool
 *
 * 挂载在 App 根节点，为所有 GlassCard/GlassButton 提供可复用的 SVG 滤镜：
 *   1. 折射滤镜（feDisplacementMap + feTurbulence）—— 边缘光线弯曲
 *   2. 镜面高光（feSpecularLighting）—— 表面光泽
 *   3. 色散分离（Chromatic Aberration 通过分层 feOffset 模拟）
 *
 * 这些滤镜通过 CSS `backdrop-filter: url(#resonance-glass-refraction)` 引用，
 * 无需每个组件实例化自己的 SVG 节点。
 */
export function LiquidGlassShaderPool() {
  return (
    <svg
      id="resonance-shader-pool"
      style={{
        position: "absolute",
        width: 0,
        height: 0,
        overflow: "hidden",
        pointerEvents: "none",
      }}
      aria-hidden="true"
    >
      <defs>
        {/* ── 折射滤镜：边缘玻璃弯曲效果 ── */}
        <filter id="resonance-glass-refraction" x="-10%" y="-10%" width="120%" height="120%">
          {/* 生成微噪点（模拟玻璃表面的微小不规则） */}
          <feTurbulence
            type="fractalNoise"
            baseFrequency="0.015 0.015"
            numOctaves={3}
            seed={42}
            stitchTiles="stitch"
            result="noise"
          />
          {/* 用噪点扭曲背景（折射效果） */}
          <feDisplacementMap
            in="SourceGraphic"
            in2="noise"
            scale={3}
            xChannelSelector="R"
            yChannelSelector="G"
            result="refracted"
          />
        </filter>

        {/* ── 镜面高光滤镜 ── */}
        <filter id="resonance-glass-specular">
          <feTurbulence
            type="fractalNoise"
            baseFrequency="0.025"
            numOctaves={2}
            seed={7}
            result="specNoise"
          />
          <feSpecularLighting
            in="specNoise"
            surfaceScale={2}
            specularConstant={0.8}
            specularExponent={25}
            lightingColor="#ffffff"
            result="specLight"
          >
            <fePointLight x={200} y={100} z={300} />
          </feSpecularLighting>
          <feComposite
            in="specLight"
            in2="SourceAlpha"
            operator="in"
            result="specComposed"
          />
          <feBlend
            in="specComposed"
            in2="SourceGraphic"
            mode="screen"
          />
        </filter>

        {/* ── 轻微色散（棱镜分色效果，用于按钮高光边缘） ── */}
        <filter id="resonance-glass-chromatic" x="-2%" y="-2%" width="104%" height="104%">
          {/* 红通道偏移 */}
          <feOffset in="SourceGraphic" dx={0.6} dy={0} result="redShift" />
          <feColorMatrix
            in="redShift"
            type="matrix"
            values="1 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 0 1 0"
            result="redOnly"
          />
          {/* 蓝通道偏移 */}
          <feOffset in="SourceGraphic" dx={-0.6} dy={0} result="blueShift" />
          <feColorMatrix
            in="blueShift"
            type="matrix"
            values="0 0 0 0 0  0 0 0 0 0  0 0 1 0 0  0 0 0 1 0"
            result="blueOnly"
          />
          {/* 绿通道不偏移 */}
          <feColorMatrix
            in="SourceGraphic"
            type="matrix"
            values="0 0 0 0 0  0 1 0 0 0  0 0 0 0 0  0 0 0 1 0"
            result="greenOnly"
          />
          {/* 合成 RGB 通道 */}
          <feBlend in="redOnly" in2="greenOnly" mode="screen" result="rg" />
          <feBlend in="rg" in2="blueOnly" mode="screen" />
        </filter>

        {/* ── 按钮级轻量折射（更细腻的噪点，适合小元素） ── */}
        <filter id="resonance-glass-button-refraction" x="-5%" y="-5%" width="110%" height="110%">
          <feTurbulence
            type="fractalNoise"
            baseFrequency="0.04 0.04"
            numOctaves={2}
            seed={13}
            stitchTiles="stitch"
            result="btnNoise"
          />
          <feDisplacementMap
            in="SourceGraphic"
            in2="btnNoise"
            scale={1.5}
            xChannelSelector="R"
            yChannelSelector="G"
          />
        </filter>
      </defs>
    </svg>
  );
}
