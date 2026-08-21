// domainlayout —— 领域卡的确定性力导向布局。
//
// 职责：给一层领域全景算出每张卡的左上角坐标
// 边界：纯函数、**不许用随机数**（Math.random / Date.now 都不行）——同一份数据
// 每次打开必须长得一样，用户的肌肉记忆和截图对照都依赖这一点。
//
// 三种力：领域卡是宽扁的，所以斥力按椭圆距离算（横向 340、纵向 200 为半径）；
// 有调用关系的领域用弹簧拉近，调用越密拉得越紧（权重封顶 4，避免一条超密的边
// 把整张图压成一团）；再加一点纵向重力把整体收在可视区里。
import type { DomainAgg } from './domains'

const ITER = 240        // 迭代次数：够收敛且在几毫秒内跑完
const RX = 340          // 椭圆斥力横向半径 ≈ 卡宽 + 间距
const RY = 200          // 纵向半径 ≈ 卡高 + 间距
const REST = 340        // 弹簧静止长度
const GRAVITY_Y = 330   // 纵向重力的目标带

export function layoutDomains(
  agg: DomainAgg,
  ids: string[],
  seed: Record<string, [number, number]> = {},
): Record<string, [number, number]> {
  const pos: Record<string, [number, number]> = {}
  ids.forEach((id, i) => {
    // 无 seed 时用 id 序号散开：质数步长让初值分散又完全确定
    pos[id] = seed[id] ? [seed[id][0], seed[id][1]] : [340 + ((i * 173) % 640), 90 + ((i * 257) % 420)]
  })
  const springs: [string, string, number][] = []
  for (const de of agg.edges.values()) {
    if (pos[de.from] && pos[de.to]) springs.push([de.from, de.to, Math.min(de.pairs.length, 4)])
  }
  for (let it = 0; it < ITER; it++) {
    const f: Record<string, [number, number]> = {}
    for (const id of ids) f[id] = [0, 0]
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const A = ids[i]
        const B = ids[j]
        const dx = pos[A][0] - pos[B][0]
        const dy = pos[A][1] - pos[B][1]
        const nd = Math.sqrt((dx / RX) ** 2 + (dy / RY) ** 2) || 0.01
        if (nd >= 1) continue
        const len = Math.hypot(dx, dy) || 1
        const push = (1 - nd) * 46
        f[A][0] += (dx / len) * push
        f[A][1] += (dy / len) * push
        f[B][0] -= (dx / len) * push
        f[B][1] -= (dy / len) * push
      }
    }
    for (const [a, b, w] of springs) {
      const dx = pos[b][0] - pos[a][0]
      const dy = pos[b][1] - pos[a][1]
      const len = Math.hypot(dx, dy) || 1
      const pull = (len - REST) * 0.012 * w
      f[a][0] += (dx / len) * pull
      f[a][1] += (dy / len) * pull
      f[b][0] -= (dx / len) * pull
      f[b][1] -= (dy / len) * pull
    }
    // 后半程减半阻尼：先快速铺开、再稳下来，避免末尾还在抖
    const damp = (it < 120 ? 1 : 0.5) * 0.5
    for (const id of ids) {
      f[id][1] += (GRAVITY_Y - pos[id][1]) * 0.005
      pos[id][0] = Math.max(30, pos[id][0] + f[id][0] * damp)
      pos[id][1] = Math.max(64, pos[id][1] + f[id][1] * damp)
    }
  }
  return pos
}
