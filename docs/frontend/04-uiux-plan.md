# Resonance IM — Liquid Glass UI/UX 开发指南

> 本文档是 S6~S9 UI/UX 开发的**设计提示词 + 技术指南**，定义视觉方向、全新原生 Liquid Glass 引擎的集成策略和分步交付方案。

---

## 1. 设计语言：Apple Liquid Glass × IM

### 1.1 核心视觉原则

Apple 在 WWDC 2025 引入的 Liquid Glass 设计语言，核心哲学是：

| 原则 | 含义 | 在 IM 中的映射 |
|------|------|---------------|
| **动态材质** | UI 面板半透明，折射并模糊下层内容，创造深度层级 | 侧栏、会话头部、Composer 底栏、弹窗 |
| **流动性** | 界面元素随用户交互实时 "弯曲光线"，有液态触感 | 按钮 hover/press、面板切换动画 |
| **同心圆几何** | 嵌套形状通过 padding 计算内半径，确保元素精密嵌合 | 消息气泡、圆角卡片、头像徽标 |
| **浮动导航** | 导航栏/Tab Bar 漂浮在内容层之上，独立于滚动 | 左栏顶部搜索/底部 Tab、右栏折叠 handle |

### 1.2 色彩体系

双主题支持：日间模式（晨曦/蜜桃色渐变）与夜间模式（深空/暗紫蓝渐变）。

```css
:root {
  /* 基础结构 */
  --glass-surface: rgba(255, 255, 255, 0.06);
  --glass-surface-hover: rgba(255, 255, 255, 0.10);
  --glass-border: rgba(255, 255, 255, 0.08);
  --glass-border-light: rgba(255, 255, 255, 0.15);
  
  /* 模糊值 */
  --glass-blur-sm: 12px;
  --glass-blur-md: 24px;
  --glass-blur-lg: 40px;
  
  /* 辅助/强调 */
  --accent: #6c5ce7;
  --accent-glow: rgba(108, 92, 231, 0.3);
}

@media (prefers-color-scheme: light) {
  :root {
    --glass-bg-gradient: linear-gradient(135deg, #ffecd2, #fcb69f, #a1c4fd);
    --glass-surface: rgba(255, 255, 255, 0.3);
    --glass-border: rgba(255, 255, 255, 0.4);
    --bubble-self: rgba(108, 92, 231, 0.8);
    --bubble-other: rgba(255, 255, 255, 0.6);
  }
}

@media (prefers-color-scheme: dark) {
  :root {
    --glass-bg-gradient: linear-gradient(135deg, #0f0c29, #302b63, #24243e);
    --bubble-self: rgba(43, 82, 120, 0.65);
    --bubble-other: rgba(255, 255, 255, 0.08);
  }
}
```

---

## 2. 纯原生 Liquid Glass 引擎架构

### 2.1 技术栈演进
在尝试过第三方库并因性能/布局不可控而放弃后，基于原生 CSS 构建：
- **纯净 DOM**：直接控制 `div`，彻底杜绝对 DOM 树不可控的包装破坏。
- **SVG Shader Pool**：全局静态挂载的滤镜池 (`#resonance-glass-refraction`)。
- **物理脱钩**：靠 `useLiquidGlass` 捕捉物理变量。

### 2.2 能力边界 (物理变量到视觉效果的映射)

| 能力 | 实现方式 | 性能影响 |
|------|------|---------|
| **深度透射/散景** | `backdrop-filter: blur(n)` + `saturate` | 中低 (利用硬件混合层) |
| **真实光线折射** | 引用共享全局池的 `feDisplacementMap` + `feTurbulence` | 中 |
| **阻尼果冻物理形变** | 原生 `useLiquidGlass` Hook 抛出 CSS `--lg-tx/ty/scale` 等 | 极低（RAF + 纯数值计算） |
| **3D 斜切反光跟踪** | 利用 `--lg-glow-x` 定位 `radial-gradient` 的坐标，配合 `mix-blend-mode: overlay` | 低 |

### 2.3 推荐用法：物理层剥离、多层光学叠加

```
❌ 错误用法：在业务组件中满篇写各种乱七八糟的 style 动效。
✅ 正确用法：基于 `GlassCard` 的体系直接复用（含 `__refraction`, `__ambient`, `__specular`, `__rim` 四层光学堆叠）。
```

---

## 3. 分步交付与技术实施

不直接在 feature 组件中使用 `<LiquidGlass>`。在 `components/` 层封装：

```
components/
├── GlassCard.tsx         # 通用玻璃卡片（large, 登录/弹窗场景）
├── GlassButton.tsx       # LiquidGlass 按钮
├── GlassSurface.tsx      # 纯 CSS 玻璃面板（侧栏/头部/Composer）
├── GlassInput.tsx        # 玻璃风格输入框（纯 CSS）
├── GlassBadge.tsx        # 角标/药丸（纯 CSS）
└── Avatar.tsx            # 头像组件（支持在线状态指示器）
```

**S6: 鉴权页 + 路由（1~2 天）**
1. 建设原生 `useLiquidGlass` 物理引擎与 CSS Token 体系。
2. 实现基于多层叠加的 `WallpaperBackground` 组件。
3. 封装 `GlassCard` `GlassButton` `GlassInput`。
4. 构建 `/login` `/register` 页面，接入路由。
