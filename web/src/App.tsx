import { RouterProvider } from '@tanstack/react-router';
import { router } from './router';
import { LiquidGlassShaderPool } from './components/LiquidGlassShaderPool';

// Make sure the DOM hasn't overridden the theme unexpectedly, 
// though we handle themes dynamically through media queries or classes

export default function App() {
  return (
    <>
      {/* 全局 SVG 滤镜池 —— 为所有液态玻璃组件提供折射/高光/色散滤镜 */}
      <LiquidGlassShaderPool />
      <RouterProvider router={router} />
    </>
  );
}
