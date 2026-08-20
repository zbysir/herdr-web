"""从 pane 的 ANSI 快照里抽出输入框内容。

按 agent 类型分派，与 herdr 自己的 agent-detection manifest 保持一致：
  claude → prompt_box_body / after_last_horizontal_rule（输入框是上下横线夹的 box）
  codex  → after_last_prompt_marker（提示符之后；这里额外用背景色块收边，
           因为我们要的是精确内容而不是一个供 regex 求值的区域）
  其他   → 通用嗅探（herdr 的 manifest 里另外 17 个 agent 也没有输入区规则，
           whole_recent / osc_title / bottom_non_empty_lines 而已，无从照抄）

dim(SGR 2) 的文字算占位提示，不算内容 —— Codex 空框的
"Run /review on my current changes" 就是这么渲染的；实测两家的真实输入都不带 dim。
"""
import re

ESC  = re.compile(r"\x1b\](?:.*?)(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[A-Za-z]|\x1b[()][A-Za-z0-9]")
SGR  = re.compile(r"\x1b\[([0-9;]*)m")
BG   = re.compile(r"48;(?:2;\d+;\d+;\d+|5;\d+)")
RULE = re.compile(r"^\s*[─━┄┅┈┉]{4,}\s*$")
MARKER = re.compile(r"^[ \t]{0,4}[❯›❱]")   # 不认裸 > ，否则 markdown 引用行会被当成起点


def _dim_states(params):
    """按 SGR 规则解析参数。38/48/58 的 2;r;g;b 与 5;n 子参数必须整段消费，
    否则 38;2;153;153;153 里的 '2' 会被误判成 dim。"""
    parts, out, i = (params.split(";") if params else ["0"]), [], 0
    while i < len(parts):
        p = parts[i] or "0"
        if p in ("38", "48", "58"):
            nxt = parts[i + 1] if i + 1 < len(parts) else None
            i += 5 if nxt == "2" else 3 if nxt == "5" else 1
            continue
        if p in ("0", "22"): out.append(False)
        elif p == "2":       out.append(True)
        i += 1
    return out


def _visible(line):
    """剔除 dim 段后的纯文本。"""
    chunks, dim, pos = [], False, 0
    for m in SGR.finditer(line):
        if not dim: chunks.append(line[pos:m.start()])
        for s in _dim_states(m.group(1)): dim = s
        pos = m.end()
    if not dim: chunks.append(line[pos:])
    return ESC.sub("", "".join(chunks)).replace("\r", "").replace("\xa0", " ")


def _plain(line):
    """保留 dim 的纯文本，用于定位（横线和 marker 本身可能是 dim 的）。"""
    return ESC.sub("", line).replace("\r", "").replace("\xa0", " ")


def _anchor(plain):
    return next((i for i in range(len(plain) - 1, -1, -1) if MARKER.search(plain[i])), None)


def _box_bounds(plain, anchor):
    """claude 式：最近的上下两条横线之间。"""
    u = anchor - 1
    while u >= 0 and not RULE.match(plain[u]): u -= 1
    d = anchor + 1
    while d < len(plain) and not RULE.match(plain[d]): d += 1
    return (u + 1, d - 1) if u >= 0 and d < len(plain) else (anchor, anchor)


def _band_bounds(raw, anchor):
    """codex 式：与 marker 行同背景色的连续段。"""
    bg = [frozenset(BG.findall(l)) for l in raw]
    if not bg[anchor]:
        return (anchor, anchor)
    lo = hi = anchor
    while lo - 1 >= 0 and bg[lo - 1] == bg[anchor]: lo -= 1
    while hi + 1 < len(raw) and bg[hi + 1] == bg[anchor]: hi += 1
    return lo, hi


def _finish(vis, lo, hi):
    out = vis[lo:hi + 1]
    for k, l in enumerate(out):
        if MARKER.search(l):
            out[k] = MARKER.sub("", l, count=1)
            if out[k][:1] == " ": out[k] = out[k][1:]
            break
    out = [(l[2:] if l[:2] == "  " else l).rstrip() for l in out]
    while out and not out[0].strip(): out.pop(0)
    while out and not out[-1].strip(): out.pop()
    return "\n".join(out)


def extract(ansi_text, agent=None):
    raw   = ansi_text.split("\n")
    plain = [_plain(l) for l in raw]
    vis   = [_visible(l) for l in raw]

    anchor = _anchor(plain)
    if anchor is None:                       # 普通 shell：最后一行非空
        return next((l.rstrip() for l in reversed(plain) if l.strip()), "")

    if agent == "claude":
        lo, hi = _box_bounds(plain, anchor)
    elif agent == "codex":
        lo, hi = _band_bounds(raw, anchor)
    else:                                    # 未知 agent：先色块，再横线，最后单行
        lo, hi = _band_bounds(raw, anchor)
        if (lo, hi) == (anchor, anchor):
            lo, hi = _box_bounds(plain, anchor)
    return _finish(vis, lo, hi)
