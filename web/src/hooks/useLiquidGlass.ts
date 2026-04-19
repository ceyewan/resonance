import { useRef, useEffect, useCallback, useState } from "react";

/**
 * 液态玻璃物理引擎 Hook
 *
 * 核心原理：模拟苹果 iOS 26 Liquid Glass 的三大物理层 ——
 *   1. 弹簧阻尼位移（鼠标接近时元素微微被"吸引"）
 *   2. 3D 透视倾斜（基于鼠标在元素上的位置计算 rotateX/Y）
 *   3. 动态光标追踪高光（specular highlight 的位置与强度）
 *
 * 所有运算通过 requestAnimationFrame 驱动，不触发 React re-render，
 * 直接操纵 CSS Custom Properties 实现零 GC 压力的 60fps 动画。
 */

export interface LiquidGlassConfig {
  /** 弹簧阻尼（0~1，越小越弹） */
  damping?: number;
  /** 弹簧刚度（越大跟手越紧） */
  stiffness?: number;
  /** 3D 倾斜最大角度（deg） */
  tiltMax?: number;
  /** 透视距离（px） */
  perspective?: number;
  /** 激活范围（鼠标距离元素边缘多远开始响应，px） */
  activationRange?: number;
  /** 高光强度（0~1） */
  glowIntensity?: number;
  /** 是否启用折射偏移 */
  enableRefraction?: boolean;
  /** 按下时的缩放比例 */
  pressScale?: number;
}

const DEFAULT_CONFIG: Required<LiquidGlassConfig> = {
  damping: 0.15,
  stiffness: 0.08,
  tiltMax: 8,
  perspective: 1200,
  activationRange: 250,
  glowIntensity: 0.85,
  enableRefraction: true,
  pressScale: 0.97,
};

interface SpringState {
  // 当前值
  translateX: number;
  translateY: number;
  tiltX: number;
  tiltY: number;
  scale: number;
  glowX: number;
  glowY: number;
  glowOpacity: number;
  refractionScale: number;
  // 目标值
  targetTranslateX: number;
  targetTranslateY: number;
  targetTiltX: number;
  targetTiltY: number;
  targetScale: number;
  targetGlowX: number;
  targetGlowY: number;
  targetGlowOpacity: number;
  targetRefractionScale: number;
}

function lerp(current: number, target: number, factor: number): number {
  return current + (target - current) * factor;
}

export function useLiquidGlass<T extends HTMLElement>(
  config: LiquidGlassConfig = {}
) {
  const ref = useRef<T>(null);
  const rafId = useRef<number>(0);
  const isPressed = useRef(false);
  const isHovered = useRef(false);

  const [cfg, setCfg] = useState({ ...DEFAULT_CONFIG, ...config });

  useEffect(() => {
    setCfg({ ...DEFAULT_CONFIG, ...config });
  }, [
    config.damping,
    config.stiffness,
    config.tiltMax,
    config.perspective,
    config.activationRange,
    config.glowIntensity,
    config.enableRefraction,
    config.pressScale,
  ]);

  const spring = useRef<SpringState>({
    translateX: 0,
    translateY: 0,
    tiltX: 0,
    tiltY: 0,
    scale: 1,
    glowX: 50,
    glowY: 50,
    glowOpacity: 0,
    refractionScale: 0,
    targetTranslateX: 0,
    targetTranslateY: 0,
    targetTiltX: 0,
    targetTiltY: 0,
    targetScale: 1,
    targetGlowX: 50,
    targetGlowY: 50,
    targetGlowOpacity: 0,
    targetRefractionScale: 0,
  });

  // 全局鼠标位置（避免 setState 触发 re-render）
  const mousePos = useRef({ x: -9999, y: -9999 });

  const updateTargets = useCallback(() => {
    const el = ref.current;
    if (!el) return;

    const rect = el.getBoundingClientRect();
    const mx = mousePos.current.x;
    const my = mousePos.current.y;
    const s = spring.current;

    const centerX = rect.left + rect.width / 2;
    const centerY = rect.top + rect.height / 2;
    const deltaX = mx - centerX;
    const deltaY = my - centerY;

    // 计算鼠标到元素边缘的距离
    const edgeDistX = Math.max(0, Math.abs(deltaX) - rect.width / 2);
    const edgeDistY = Math.max(0, Math.abs(deltaY) - rect.height / 2);
    const edgeDist = Math.sqrt(edgeDistX * edgeDistX + edgeDistY * edgeDistY);

    // 激活因子（0 = 离得远，1 = 在元素上/紧贴边缘）
    const activation = Math.max(0, 1 - edgeDist / cfg.activationRange);

    // ── 弹簧位移 ──
    // 元素微微向鼠标方向偏移，像被磁力吸引
    const maxTranslate = 4;
    s.targetTranslateX = (deltaX / rect.width) * maxTranslate * activation;
    s.targetTranslateY = (deltaY / rect.height) * maxTranslate * activation;

    // ── 3D 倾斜 ──
    // 鼠标在元素内部时产生倾斜（模拟手持玻璃片倾斜看反光）
    const isInside =
      mx >= rect.left &&
      mx <= rect.right &&
      my >= rect.top &&
      my <= rect.bottom;

    if (isInside) {
      const normalX = ((mx - rect.left) / rect.width - 0.5) * 2;
      const normalY = ((my - rect.top) / rect.height - 0.5) * 2;
      // rotateY: 鼠标向右 → 面板左边抬起；rotateX: 鼠标向下 → 顶部抬起
      s.targetTiltY = normalX * cfg.tiltMax;
      s.targetTiltX = -normalY * cfg.tiltMax;
    } else {
      s.targetTiltX = 0;
      s.targetTiltY = 0;
    }

    // ── 光标高光 ──
    if (isInside) {
      s.targetGlowX = ((mx - rect.left) / rect.width) * 100;
      s.targetGlowY = ((my - rect.top) / rect.height) * 100;
      s.targetGlowOpacity = cfg.glowIntensity;
    } else {
      s.targetGlowOpacity = Math.max(0, activation * cfg.glowIntensity * 0.3);
    }

    // ── 折射强度 ──
    s.targetRefractionScale = isInside ? 1 : activation * 0.3;

    // ── 按压缩放 ──
    s.targetScale = isPressed.current ? cfg.pressScale : 1;
  }, [cfg]);

  const animate = useCallback(() => {
    const s = spring.current;
    const el = ref.current;
    if (!el) {
      rafId.current = requestAnimationFrame(animate);
      return;
    }

    updateTargets();

    // 弹簧插值
    const lerpFactor = cfg.stiffness;
    const dampFactor = cfg.damping;

    s.translateX = lerp(s.translateX, s.targetTranslateX, lerpFactor);
    s.translateY = lerp(s.translateY, s.targetTranslateY, lerpFactor);
    s.tiltX = lerp(s.tiltX, s.targetTiltX, dampFactor);
    s.tiltY = lerp(s.tiltY, s.targetTiltY, dampFactor);
    s.scale = lerp(s.scale, s.targetScale, dampFactor * 2);
    s.glowX = lerp(s.glowX, s.targetGlowX, lerpFactor * 1.5);
    s.glowY = lerp(s.glowY, s.targetGlowY, lerpFactor * 1.5);
    s.glowOpacity = lerp(s.glowOpacity, s.targetGlowOpacity, dampFactor);
    s.refractionScale = lerp(
      s.refractionScale,
      s.targetRefractionScale,
      dampFactor
    );

    // 直接写 CSS Custom Properties（不触发 React render）
    const style = el.style;
    style.setProperty("--lg-tx", `${s.translateX}px`);
    style.setProperty("--lg-ty", `${s.translateY}px`);
    style.setProperty("--lg-rx", `${s.tiltX}deg`);
    style.setProperty("--lg-ry", `${s.tiltY}deg`);
    style.setProperty("--lg-scale", `${s.scale}`);
    style.setProperty("--lg-glow-x", `${s.glowX}%`);
    style.setProperty("--lg-glow-y", `${s.glowY}%`);
    style.setProperty("--lg-glow-opacity", `${s.glowOpacity}`);
    style.setProperty("--lg-refraction", `${s.refractionScale}`);

    rafId.current = requestAnimationFrame(animate);
  }, [cfg, updateTargets]);

  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      mousePos.current = { x: e.clientX, y: e.clientY };
    };

    window.addEventListener("mousemove", handleMouseMove, { passive: true });
    rafId.current = requestAnimationFrame(animate);

    return () => {
      window.removeEventListener("mousemove", handleMouseMove);
      cancelAnimationFrame(rafId.current);
    };
  }, [animate]);

  const handlers = {
    onMouseDown: () => {
      isPressed.current = true;
    },
    onMouseUp: () => {
      isPressed.current = false;
    },
    onMouseEnter: () => {
      isHovered.current = true;
    },
    onMouseLeave: () => {
      isPressed.current = false;
      isHovered.current = false;
      // 归零目标
      const s = spring.current;
      s.targetTranslateX = 0;
      s.targetTranslateY = 0;
      s.targetTiltX = 0;
      s.targetTiltY = 0;
      s.targetScale = 1;
      s.targetGlowOpacity = 0;
      s.targetRefractionScale = 0;
    },
  };

  return { ref, handlers };
}
