# 前端开发踩坑与规范记录 (Web Frontend Gotchas & Guidelines)

## Zustand v5 状态订阅与渲染死循环 (Maximum update depth exceeded)

- **问题表现**: 在 React 19 严格模式或频繁更新的页面中，使用 Zustand store 如果直接返回对象字面量，极易触发 `Maximum update depth exceeded` 死循环。
- **根本原因**: Zustand v5 默认使用 `Object.is` 比较选出的状态。如果像 `useStore(state => ({ a: state.a, b: state.b }))` 这样每次都返回一个新的对象字面量，Zustand 和 React 会认为状态一直在“变更”，从而触发 `useSyncExternalStore` 的 `getSnapshot` 异常和无限 setState 循环。
- **正确做法**: 必须使用 `useShallow` 进行浅比较，保持引用稳定：

  ```typescript
  import { useShallow } from "zustand/react/shallow";

  export function useConnectionState() {
    return useConnectionStore(
      useShallow((state) => ({
        status: state.status,
        lastError: state.lastError,
        // ...
      }))
    );
  }
  ```
