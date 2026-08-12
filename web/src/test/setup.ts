// vitest 全局测试设置：加载 jest-dom 的 matcher（toBeDisabled / toHaveTextContent 等）。
// @testing-library/jest-dom 是项目既有 devDependency，这里只是把它接进 vitest。
//
// jest 全局桩：@testing-library/dom 的 waitFor 只在检测到全局 `jest` 时才走
// fake-timer 分支（helpers.js 的 jestFakeTimersAreEnabled）；vitest 不提供该全局，
// waitFor 会退回真实定时器，被 vi.useFakeTimers() 冻结后永久挂起。这是 vitest +
// testing-library 的已知兼容点，官方同样用这个桩。只用 advanceTimersByTime，
// waitFor 内部在 fake timer 模式下只依赖它推进轮询。
import '@testing-library/jest-dom/vitest'
import { vi } from 'vitest'

vi.stubGlobal('jest', {
  advanceTimersByTime: (ms: number) => vi.advanceTimersByTime(ms),
})
