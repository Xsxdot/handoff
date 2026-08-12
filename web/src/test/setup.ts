// vitest 全局测试设置：加载 jest-dom 的 matcher（toBeDisabled / toHaveTextContent 等）。
// @testing-library/jest-dom 是项目既有 devDependency，这里只是把它接进 vitest。
//
// jest 全局桩：@testing-library/dom 的 waitFor 只在检测到全局 `jest` 时才走
// fake-timer 分支（helpers.js 的 jestFakeTimersAreEnabled）；vitest 不提供该全局，
// waitFor 会退回真实定时器，被 vi.useFakeTimers() 冻结后永久挂起。这是 vitest +
// testing-library 的已知兼容点，官方同样用这个桩。只用 advanceTimersByTime，
// waitFor 内部在 fake timer 模式下只依赖它推进轮询。
//
// RTL 清理：@testing-library/react 的自动 cleanup 只在检测到全局 afterEach 时
// 注册（dist/index.js）；vitest 未开 globals，没有该全局，DOM 会在用例间累积，
// 导致 getByRole/getByText 报 multiple matches。这里显式注册等价清理。
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

afterEach(() => cleanup())

vi.stubGlobal('jest', {
  advanceTimersByTime: (ms: number) => vi.advanceTimersByTime(ms),
})
