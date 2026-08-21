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
const CARD_W = 252      // 与 DomainPanorama 的 CARD_W 一致
const CARD_H = 112      // 卡高实测 89~108，取上界留余量
const SEP_ITER = 80     // 分离迭代上限；正常几轮就收敛，上限只防病态输入
const EXT_W = 176       // 域外占位卡实测尺寸（比实卡小一号）
const EXT_H = 56

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
  // 域外占位卡不参与内部排布：它们是边界之外的注解，摆在外圈（见 ringOuter）
  const inner = ids.filter((id) => !agg.cards?.[id]?.ext)
  const outer = ids.filter((id) => agg.cards?.[id]?.ext)
  const springs: [string, string, number][] = []
  for (const de of agg.edges.values()) {
    if (pos[de.from] && pos[de.to] && !agg.cards?.[de.from]?.ext && !agg.cards?.[de.to]?.ext) {
      springs.push([de.from, de.to, Math.min(de.pairs.length, 4)])
    }
  }
  for (let it = 0; it < ITER; it++) {
    const f: Record<string, [number, number]> = {}
    for (const id of inner) f[id] = [0, 0]
    for (let i = 0; i < inner.length; i++) {
      for (let j = i + 1; j < inner.length; j++) {
        const A = inner[i]
        const B = inner[j]
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
    for (const id of inner) {
      f[id][1] += (GRAVITY_Y - pos[id][1]) * 0.005
      pos[id][0] = Math.max(30, pos[id][0] + f[id][0] * damp)
      pos[id][1] = Math.max(64, pos[id][1] + f[id][1] * damp)
    }
  }
  separate(pos, inner)
  ringOuter(agg, pos, inner, outer)
  separate(pos, ids, new Set(inner))
  return pos
}

// ringOuter 把域外占位卡摆到本层内容外面的一圈上。
//
// 为什么不和本层内容一起做力导向：混在一起摆出来的图读不出「里」和「外」——
// 真机上进「本机治理」那一层，2 张本层卡被 5 张域外卡夹在中间，看起来像 7 个
// 领域乱摆。占位卡的作用是「这条调用出去到哪儿了」，属于边界之外的注解，
// 位置应该在外圈、按它连向谁来定方位，而不是参与内部的排布。
function ringOuter(
  agg: DomainAgg,
  pos: Record<string, [number, number]>,
  inner: string[],
  outer: string[],
): void {
  if (!outer.length) return
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
  for (const id of inner) {
    const p = pos[id]
    if (!p) continue
    x0 = Math.min(x0, p[0]); y0 = Math.min(y0, p[1])
    x1 = Math.max(x1, p[0] + CARD_W); y1 = Math.max(y1, p[1] + CARD_H)
  }
  if (!Number.isFinite(x0)) { x0 = 60; y0 = 90; x1 = 60 + CARD_W; y1 = 90 + CARD_H }
  const cx = (x0 + x1) / 2
  const cy = (y0 + y1) / 2
  const rx = (x1 - x0) / 2 + 260
  const ry = (y1 - y0) / 2 + 190
  // 方位取「它连向的本层卡」的平均方向：调用谁就落在谁那一侧，连线不用横穿全场
  const dir: Record<string, [number, number]> = {}
  for (const de of agg.edges.values()) {
    for (const [a, b] of [[de.from, de.to], [de.to, de.from]]) {
      if (!outer.includes(a) || !pos[b]) continue
      const d = dir[a] ?? (dir[a] = [0, 0])
      d[0] += pos[b][0] + CARD_W / 2 - cx
      d[1] += pos[b][1] + CARD_H / 2 - cy
    }
  }
  outer.forEach((id, i) => {
    const d = dir[id]
    // 没有连线的（理论上不该出现）按序号均分一圈，保持确定性
    const ang = d && (d[0] || d[1])
      ? Math.atan2(d[1], d[0])
      : (i / outer.length) * Math.PI * 2
    pos[id] = [
      Math.max(30, cx + Math.cos(ang) * rx - EXT_W / 2),
      Math.max(64, cy + Math.sin(ang) * ry - EXT_H / 2),
    ]
  })
}

// separate 在力导向收敛后，按**矩形真实相交**再分离一遍。
//
// 为什么单靠椭圆斥力不够：斥力是软的，领域一多（真机上 36 个）就在重力压出的
// 横带里解不干净——36 张卡里 23 对压在一起，而卡压卡是这张图唯一不可接受的形态
// 问题（读不出来就等于没有）。这里沿最小平移向量把相交的两张各挪一半，直到没有
// 相交或到迭代上限。
//
// 确定性：只按 ids 的顺序两两处理，不含随机数——同一份数据每次结果逐位相同。
function separate(pos: Record<string, [number, number]>, ids: string[], frozen?: Set<string>): void {
  for (let it = 0; it < SEP_ITER; it++) {
    let moved = false
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const A = pos[ids[i]]
        const B = pos[ids[j]]
        const ox = Math.min(A[0] + CARD_W, B[0] + CARD_W) - Math.max(A[0], B[0])
        const oy = Math.min(A[1] + CARD_H, B[1] + CARD_H) - Math.max(A[1], B[1])
        if (ox <= 0 || oy <= 0) continue
        moved = true
        // 冻结的一侧不动，位移全给另一侧；两侧都冻结就跳过（本层内容之间已分离过）
        const fa = frozen?.has(ids[i]) ?? false
        const fb = frozen?.has(ids[j]) ?? false
        if (fa && fb) continue
        const share = fa || fb ? 1 : 0.5
        // 沿重叠较小的那个轴分开：挪动距离最小，布局形状改变也最小
        if (ox < oy) {
          const d = (ox * share + 1) * (A[0] <= B[0] ? -1 : 1)
          if (!fa) A[0] = Math.max(30, A[0] + d)
          if (!fb) B[0] = Math.max(30, B[0] - d)
        } else {
          const d = (oy * share + 1) * (A[1] <= B[1] ? -1 : 1)
          if (!fa) A[1] = Math.max(64, A[1] + d)
          if (!fb) B[1] = Math.max(64, B[1] - d)
        }
      }
    }
    if (!moved) return
  }
}
