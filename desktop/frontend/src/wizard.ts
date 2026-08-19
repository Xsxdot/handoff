import {Events} from "@wailsio/runtime";

// 首次配置单页表单：一次性接收字段表（wizard-form）渲染成单页表单，
// 用户点「完成」后把答案整体发回（wizard-submit）。
//
// 职责：
//   - 把 Go 侧 initflow 的字段表渲染成单页表单：常显区 + 默认折叠的「高级设置」
//   - 依据字段数据（roles / show_when / default_when / advanced）计算显隐与
//     默认值，**不内嵌任何字段名的分支规则**——规则真相只在 Go 侧 initflow
//   - 收集答案并在提交时带上每个当前可见字段（含折叠区里没碰过的字段，取默认值）
//   - 监听 wizard-notice：把一般提示（如检测到旧版已装 handoff）作为非阻塞信息
//     横幅显示在表单顶部——提示随时可能到达（甚至早于表单），独立于表单内容渲染、
//     不清空已填内容
//
// 边界：
//   - 不校验答案：合法性校验与写盘都在 Go 侧 Apply，本文件只负责把选/填的值送回去
//   - 不做持久化：全部状态都在内存，关窗重开即重来

// === 类型：与 internal/initflow 的 Field/Cond 一一对应 ===
// 键名是**对外契约**：Go 侧已加 json tag（value/label 等），改名等于改协议。
interface Option { value: string; label: string }
interface Cond { key: string; equal?: string; in?: string[]; non_empty?: boolean }
interface DefaultRule { cond: Cond; value: string }
interface Field {
    key: string
    kind: 'select' | 'input' | 'confirm'
    title: string
    notice: string
    default: string
    options?: Option[]
    roles?: string[]
    advanced: boolean
    show_when?: Cond | null
    default_when?: DefaultRule[]
}

// === 求值：与 Go 侧 matchCond / roleMatches / DefaultOf / Visible 同构 ===
// 显隐与默认值的唯一真相，改动必须与 internal/initflow/form.go 同步。

// 答案缺键时按空串处理，与 Go 的 map 取值一致。
function match(c: Cond, ans: Record<string, string>): boolean {
    const v = ans[c.key] ?? "";
    if (c.non_empty) return v !== "";
    if (c.in && c.in.length > 0) return c.in.includes(v);
    return v === (c.equal ?? "");
}

// roles 空或缺省=恒显示；role 为 both 时 executor/coordinator 都算命中，
// 与 Go 的 roleMatches 一致。
function roleMatches(roles: string[] | undefined, role: string): boolean {
    if (!roles || roles.length === 0) return true;
    for (const r of roles) {
        if (r === role || (role === "both" && (r === "executor" || r === "coordinator"))) return true;
    }
    return false;
}

// 命中第一条 default_when 返回其 value，否则用 f.default。
function defaultOf(f: Field, ans: Record<string, string>): string {
    for (const r of f.default_when ?? []) {
        if (match(r.cond, ans)) return r.value;
    }
    return f.default;
}

// 先判角色是否适用，再判 show_when——顺序与 Go 的 Visible 一致。
function visible(f: Field, ans: Record<string, string>): boolean {
    if (!roleMatches(f.roles, ans["role"])) return false;
    if (f.show_when) return match(f.show_when, ans);
    return true;
}

// === 渲染 ===
const box = document.getElementById("question")!;

// wizard-notice 横幅容器：独立于表单内容区，因为提示随时可能到达（甚至早于
// wizard-form），不能被 redraw 重建或清掉。复用 index.html 里现成的 #notices
// （此前闲置），并挪到表单区上方，让横幅显示在表单顶部；无提示时隐藏不占位。
const notices = document.getElementById("notices")!;
box.before(notices);
notices.hidden = true;

let fields: Field[] = [];
let answers: Record<string, string> = {};
let touched = new Set<string>();  // 用户手动改过的字段：重绘后不再按 defaultOf 覆盖
let lastError = "";
let submitted = false;
// advancedOpen 记住「高级设置」的展开状态。
//
// redraw 每次都整体重建 DOM，而 <details> 的展开状态是 DOM 上的，不带着它
// 走就会丢：在折叠区里改任何一个 select（比如选审批链执行者）都会触发重绘，
// 面板当场收起来、还得再点开一次。真机走查实测撞到过。
let advancedOpen = false;

// 渲染单个字段：标题、说明（notice 在控件上方）、控件。
function fieldNode(f: Field): HTMLElement {
    const id = "w-field-" + f.key;
    const div = document.createElement("div");
    div.className = "field";

    const label = document.createElement("label");
    label.className = "field-title";
    label.textContent = f.title;
    label.htmlFor = id;
    div.appendChild(label);

    if (f.notice) {
        const p = document.createElement("p");
        p.className = "notice";
        p.textContent = f.notice;
        div.appendChild(p);
    }

    const value = answers[f.key] ?? defaultOf(f, answers);

    if (f.kind === "select") {
        const sel = document.createElement("select");
        sel.id = id;
        for (const o of f.options ?? []) {
            const opt = document.createElement("option");
            opt.value = o.value;
            opt.textContent = o.label;
            sel.appendChild(opt);
        }
        sel.value = value;
        sel.addEventListener("change", () => {
            answers[f.key] = sel.value;
            touched.add(f.key);
            redraw();
        });
        div.appendChild(sel);
    } else if (f.kind === "confirm") {
        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.id = id;
        cb.checked = value === "true";
        cb.addEventListener("change", () => {
            answers[f.key] = cb.checked ? "true" : "false";
            touched.add(f.key);
            redraw();
        });
        div.appendChild(cb);
    } else {
        const inp = document.createElement("input");
        inp.type = "text";
        inp.id = id;
        inp.value = value;
        // 文本输入只写 answers、不触发重绘：重绘会重建 DOM 丢掉输入焦点，
        // 连续打字会每敲一个字符丢一次焦点。可见性只由 select/confirm 的答案
        // 决定，文本值不参与显隐判断，所以不重绘也不会漏算。
        inp.addEventListener("input", () => {
            answers[f.key] = inp.value;
            touched.add(f.key);
        });
        div.appendChild(inp);
    }

    return div;
}

function redraw() {
    // 先为未手动改过的字段推导默认值（监听预设随角色翻档正是这条规则的产物），
    // 再据最新 answers 算可见性：默认值更新可能影响后续字段的显隐。
    for (const f of fields) {
        if (!touched.has(f.key)) answers[f.key] = defaultOf(f, answers);
    }
    const vis = fields.map(f => visible(f, answers));

    const form = document.createElement("form");
    form.id = "wizard-form";
    form.noValidate = true;

    // advanced=false 的字段进常显区，advanced=true 的字段进默认折叠的「高级设置」。
    for (let i = 0; i < fields.length; i++) {
        if (vis[i] && !fields[i].advanced) form.appendChild(fieldNode(fields[i]));
    }
    const hasAdvanced = fields.some((f, i) => vis[i] && f.advanced);
    if (hasAdvanced) {
        const det = document.createElement("details");
        det.className = "advanced";
        det.open = advancedOpen;
        det.addEventListener("toggle", () => { advancedOpen = det.open; });
        const sum = document.createElement("summary");
        sum.textContent = "高级设置";
        det.appendChild(sum);
        for (let i = 0; i < fields.length; i++) {
            if (vis[i] && fields[i].advanced) det.appendChild(fieldNode(fields[i]));
        }
        form.appendChild(det);
    }

    if (lastError) {
        const err = document.createElement("p");
        err.className = "wizard-error";
        // 错误原文 + 出路提示：Go 侧校验/保存失败后向导已终结（见
        // wizard-error 回调注释），按钮保持禁用，唯一出路是关窗重开。
        err.textContent = lastError;
        err.appendChild(document.createElement("br"));
        err.appendChild(document.createTextNode("配置未保存，请关闭窗口后重新打开以重试。"));
        form.appendChild(err);
    }

    const btn = document.createElement("button");
    btn.type = "submit";
    btn.textContent = "完成";
    btn.disabled = submitted;
    form.appendChild(btn);

    form.addEventListener("submit", (e) => {
        e.preventDefault();
        doSubmit();
    });

    box.innerHTML = "";
    box.appendChild(form);
}

function doSubmit() {
    if (submitted) return;
    submitted = true;

    // 文本输入的值已随 input 事件写入 answers，这里再兜底读一次 DOM，
    // 覆盖「改完文本但未触发事件就点完成」的极端情况。
    const form = document.getElementById("wizard-form");
    if (form) {
        form.querySelectorAll<HTMLInputElement>('input[type="text"]').forEach(inp => {
            answers[inp.id.replace(/^w-field-/, "")] = inp.value;
        });
    }

    // 必须带上每个当前可见字段——折叠在高级设置里、用户一次没碰过的
    // 字段也以 defaultOf 值参与提交。不可见字段不带，Go 侧 Apply 会忽略。
    const payload: Record<string, string> = {};
    for (let i = 0; i < fields.length; i++) {
        if (visible(fields[i], answers)) {
            payload[fields[i].key] = answers[fields[i].key] ?? defaultOf(fields[i], answers);
        }
    }

    const btn = document.querySelector<HTMLButtonElement>("#wizard-form button[type='submit']");
    if (btn) btn.disabled = true;  // 防双发
    Events.Emit("wizard-submit", payload);
}

// 事件回调的 payload 被 Wails 事件模板包在事件对象里：ev.data 才是字段表。
Events.On("wizard-form", (ev: {data: Field[]}) => {
    fields = ev.data;
    answers = {};
    touched = new Set();
    lastError = "";
    submitted = false;
    redraw();
});

Events.On("wizard-error", (ev: {data: string}) => {
    lastError = ev.data;
    // Go 侧 ApplyAnswers / Save 失败后向导流程已终结：无人再读 wizard-submit
    // 通道，此时恢复按钮只会让下一次「完成」把消息发进没人接收的通道、被静默
    // 丢弃，页面看似冻结。所以 submitted 保持 true、按钮保持禁用，提示文案已
    // 写明唯一出路——关窗重开。重绘不清空已填内容：answers/touched 原样保留。
    redraw();
});

// wizard-notice 可能在任何时刻到达（wizard-form 之前/期间/之后），是一站式
// 启动提示（如检测到旧版 handoff），非阻塞信息：不触发重绘、不清空已填内容。
// 新 notice 覆盖旧的、不累积。
Events.On("wizard-notice", (ev: {data: string}) => {
    notices.innerHTML = "";
    const banner = document.createElement("div");
    banner.className = "wizard-notice";
    banner.textContent = ev.data;
    notices.appendChild(banner);
    notices.hidden = false;
});

Events.On("wizard-done", () => {
    box.innerHTML = "";
    const h = document.createElement("h2");
    h.textContent = "配置完成";
    box.appendChild(h);
});
