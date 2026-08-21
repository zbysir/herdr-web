#!/usr/bin/env python3
"""重新生成 herdr-web 的图标（web/public/ 和 assets/ 里那几个文件）。

    python3 assets/make-logo.py

为什么要个脚本而不是手改 SVG：羊的剪影是从 herdr 的 assets/logo.svg 里复用过来的
一条 1800 多字符的描图路径（potrace 输出），手工改它不现实；而同一份图形要出
圆角版（favicon / 页面里）、方角版（iOS 和 Android 会自己套圆角遮罩，自己再圆
一次会露出两层圆角）和三种尺寸的 png，只有生成靠得住。

图形本身：herdr 的羊关在一个浏览器窗口里 —— 产品就是这一句「浏览器里的 herdr」。
羊脸上那个 >_ 是 herdr 原本就挖空的，这里在剪影**底下**垫一块品牌绿，挖空处就自然
透出绿色。垫片被羊的实心外轮廓（把两个洞去掉的那份路径）clip 掉，所以绿色绝不会
漏到轮廓外面 —— 直接摆一块绿矩形上去是会漏的（耳朵和脑门那儿的缺口）。

png 需要 rsvg-convert（brew install librsvg）；没有就只出 svg。
"""
import os
import subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
PUB = os.path.join(REPO, "web", "public")

# herdr 的羊（assets/logo.svg 里那条路径，原样搬过来）。两个 " m" 开头的子路径是
# 脸上的 > 和 _，也就是那两个洞。
RAM = (
    "M2794 3710 c-129 -33 -299 -135 -359 -214 -21 -28 -26 -42 -21 -63 9 -38 154 -178 199 -192 32 -11 41 -9 104 23 171 86 354 70 475 -43 150 -138 150 -379 0 -511 -107 -95 -278 -94 -386 2 l-46 40 -11 -29 c-16 -40 -14 -122 4 -164 60 -144 264 -222 452 -174 360 92 559 494 430 868 -36 103 -81 173 -175 267 -71 72 -100 93 -180 132 -52 26 -127 54 -167 62 -96 21 -230 20 -319 -4z M2183 3695 c-116 -32 -221 -108 -273 -199 -17 -28 -30 -54 -30 -58 0 -4 20 1 45 12 66 28 220 68 294 76 64 7 65 7 137 83 40 41 71 77 69 79 -2 2 -21 8 -42 13 -55 12 -140 10 -200 -6z M2212 3388 c-159 -22 -390 -122 -559 -241 -299 -210 -585 -600 -609 -828 -12 -118 40 -251 125 -318 96 -76 178 -98 426 -116 110 -8 224 -21 254 -29 125 -34 230 -115 272 -211 11 -24 24 -81 30 -127 20 -170 65 -271 166 -374 34 -35 63 -65 63 -67 0 -1 -10 -27 -22 -57 -29 -76 -37 -259 -14 -350 42 -170 158 -318 311 -397 44 -23 98 -46 120 -52 22 -6 45 -14 51 -18 5 -5 15 -48 22 -96 6 -48 14 -92 17 -97 4 -6 415 -10 1131 -10 l1124 0 0 1584 0 1585 -55 -19 c-84 -29 -143 -68 -232 -154 l-83 -78 -54 49 c-111 102 -233 151 -391 160 -113 6 -199 -10 -298 -54 l-60 -27 -26 34 c-37 51 -120 134 -127 128 -3 -4 0 -34 7 -67 17 -89 7 -268 -21 -356 -103 -328 -377 -545 -688 -545 -161 0 -273 41 -373 137 -37 35 -66 75 -85 116 -79 173 -8 407 124 407 44 0 68 -14 117 -66 74 -78 167 -82 238 -9 60 62 72 147 33 231 -42 90 -120 130 -238 122 -50 -4 -85 -14 -131 -37 -89 -45 -122 -52 -176 -40 -92 20 -262 163 -302 253 -11 25 -21 45 -22 45 -1 -1 -30 -6 -65 -11z m-256 -505 c115 -88 129 -102 132 -131 2 -18 -2 -40 -9 -49 -7 -8 -69 -55 -138 -105 -103 -73 -130 -88 -151 -83 -35 8 -51 34 -48 74 3 31 12 42 83 92 44 31 80 61 82 66 1 4 -30 31 -69 58 -39 28 -77 56 -85 63 -18 19 -16 72 4 94 32 35 63 23 199 -79z m528 -88 c23 -24 28 -52 14 -82 l-13 -28 -141 -3 c-130 -2 -142 -1 -158 17 -23 26 -24 66 -1 91 16 18 32 20 151 20 105 0 136 -3 148 -15z"
)
SOLID = RAM.split(" m")[0]  # 去掉两个洞 → 实心外轮廓，给绿垫片当 clip

GREEN = "#3ecf8e"   # --color-brand
SNOW = "#ededed"    # --color-fg（暗色）
INK = "#26292c"     # 亮色版的羊，比 herdr 的 #303438 深一点，配绿更沉
HERDR_GRAY = "#d9dad8"  # herdr 图标的底色，亮色版沿用它当「页面」

NOTE = """<!--
  别手改这个文件：它是 assets/make-logo.py 生成的（羊的剪影是 herdr assets/logo.svg
  里那条描图路径，1800+ 字符，手改不现实）。要调构图就改那个脚本再跑一遍。
-->
"""


def icon(chrome, page, ink, rx, uid, s=1.6, tx=-13.0, ty=1.0):
    """一枚图标。rx=0 出方角版（给 iOS / Android 的遮罩用）。

    s/tx/ty 是羊在窗口里的缩放和位移：方角版能塞得更满，圆角版要给圆角留点边，
    不然羊的下巴会被切掉一块。
    """
    pg = (
        f"M0 12.5 H64 V{64 - rx} A{rx} {rx} 0 0 1 {64 - rx} 64 H{rx} A{rx} {rx} 0 0 1 0 {64 - rx} Z"
        if rx
        else "M0 12.5 H64 V64 H0 Z"
    )
    return (
        f'  <rect width="64" height="64" rx="{rx}" fill="{chrome}"/>\n'
        f'  <path id="pg{uid}" d="{pg}" fill="{page}"/>\n'
        f'  <clipPath id="cp{uid}"><use href="#pg{uid}"/></clipPath>\n'
        # 窗口顶栏那三个点：最右边那个是绿的，等于「连着」。图标缩到 16px 时脸上的
        # 提示符已经糊了，这个点是最后还认得出品牌色的东西。
        f'  <circle cx="11" cy="6.4" r="2.1" fill="{ink}" opacity=".3"/>\n'
        f'  <circle cx="18" cy="6.4" r="2.1" fill="{ink}" opacity=".3"/>\n'
        f'  <circle cx="25" cy="6.4" r="2.1" fill="{GREEN}"/>\n'
        f'  <g clip-path="url(#cp{uid})">\n'
        f'    <clipPath id="sh{uid}"><path transform="translate(0 512) scale(.1 -.1)" d="{SOLID}"/></clipPath>\n'
        f'    <g transform="translate({tx} {ty}) scale({s * 64 / 512})">\n'
        f'      <rect x="150" y="195" width="120" height="85" fill="{GREEN}" clip-path="url(#sh{uid})"/>\n'
        f'      <g fill="{ink}" transform="translate(0 512) scale(.1 -.1)"><path d="{RAM}"/></g>\n'
        f"    </g>\n  </g>"
    )


def write(path, body, note=True):
    open(path, "w").write(
        '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64" '
        f'role="img" aria-label="herdr-web logo">\n{NOTE if note else ""}{body}\n</svg>\n'
    )
    print("→", os.path.relpath(path, REPO))


def main():
    os.makedirs(PUB, exist_ok=True)
    dark = icon("#2b2b2b", "#191919", SNOW, 14, "d")
    light = icon("#c6c7c5", HERDR_GRAY, INK, 14, "l")
    for d in (PUB, HERE):
        write(os.path.join(d, "logo.svg"), dark)
        write(os.path.join(d, "logo-light.svg"), light)

    square = os.path.join(HERE, ".square.svg")  # 只是 png 的中间产物
    write(square, icon("#2b2b2b", "#191919", SNOW, 0, "sq", s=1.72, tx=-14, ty=-2), note=False)
    try:
        for name, size, out in [
            ("apple-touch-icon.png", 180, PUB),
            ("icon-192.png", 192, PUB),
            ("icon-512.png", 512, PUB),
            ("logo.png", 512, HERE),
        ]:
            subprocess.run(
                ["rsvg-convert", "-w", str(size), "-h", str(size), square, "-o", os.path.join(out, name)],
                check=True,
            )
            print("→", os.path.relpath(os.path.join(out, name), REPO))
    except (FileNotFoundError, subprocess.CalledProcessError) as e:
        print("png 没出（需要 rsvg-convert：brew install librsvg）：", e)
    finally:
        os.path.exists(square) and os.remove(square)


if __name__ == "__main__":
    main()
