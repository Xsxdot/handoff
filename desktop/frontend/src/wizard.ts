import {Events} from "@wailsio/runtime";

interface Option { Value: string; Label: string }
interface Question { Kind: "select" | "input" | "confirm"; Title: string; Default: string; Options?: Option[] }

const box = document.getElementById("question")!;
const notices = document.getElementById("notices")!;

// 每一问渲染成一组控件，用户提交后把答案原样发回 Go。
// 空提交发空串——Go 侧 EventPrompter 会落回默认值，
// 与 CLI「一路回车保持不变」的行为一致。
function render(q: Question) {
    box.innerHTML = "";
    const h = document.createElement("h2");
    h.textContent = q.Title;
    box.appendChild(h);

    let read: () => string;

    if (q.Kind === "select") {
        const sel = document.createElement("select");
        for (const o of q.Options ?? []) {
            const opt = document.createElement("option");
            opt.value = o.Value;
            opt.textContent = o.Label;
            if (o.Value === q.Default) opt.selected = true;
            sel.appendChild(opt);
        }
        box.appendChild(sel);
        read = () => sel.value;
    } else if (q.Kind === "confirm") {
        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.checked = q.Default === "true";
        box.appendChild(cb);
        read = () => String(cb.checked);
    } else {
        const inp = document.createElement("input");
        inp.type = "text";
        inp.value = q.Default;
        box.appendChild(inp);
        read = () => inp.value;
        inp.addEventListener("keydown", (e) => {
            if ((e as KeyboardEvent).key === "Enter") submit();
        });
    }

    const btn = document.createElement("button");
    btn.textContent = "下一步";
    btn.addEventListener("click", () => submit());
    box.appendChild(btn);

    function submit() {
        btn.disabled = true;          // 防重复提交：一问一答，多发一次会错位
        Events.Emit("wizard-answer", read());
    }
}

Events.On("wizard-question", (ev: {data: Question}) => render(ev.data));

Events.On("wizard-notice", (ev: {data: string}) => {
    const p = document.createElement("p");
    p.textContent = ev.data;
    notices.appendChild(p);
});

Events.On("wizard-done", () => {
    box.innerHTML = "<h2>配置完成，正在启动 agentd…</h2>";
});
