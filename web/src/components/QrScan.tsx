import { useEffect, useRef, useState } from 'react'
import { Button } from './ui/button'

/**
 * 页面内扫二维码：开后摄、逐帧丢给平台自带的解码器、认出配对码就交出去。
 *
 * 为什么用 `BarcodeDetector` 而不是打包一个 JS 解码库：这东西是系统提供的（macOS 走
 * Vision、Android 走 ML Kit），几十 KB 的包和几毫秒的解码都省了。代价是**不是哪儿都
 * 有** —— iOS Safari 至今没有，Linux 上的 Chrome 也没有。所以这个入口是**能用才出现**
 * （`qrScanSupported`），不能用的时候页面上仍然是「抄那 8 位码」，不留一个点了报错的按钮。
 *
 * 另一条硬约束：摄像头只在**安全上下文**（https / localhost）里给。局域网 http 下浏览器
 * 直接不给 `getUserMedia`，这也算在 supported 里判掉。
 */

/** BarcodeDetector 还没进 lib.dom，本地补一个最小声明 */
type Hit = { rawValue: string }
type Detector = { detect: (src: CanvasImageSource) => Promise<Hit[]> }
type DetectorCtor = new (o?: { formats?: string[] }) => Detector

const detectorCtor = () => (window as unknown as { BarcodeDetector?: DetectorCtor }).BarcodeDetector

export const qrScanSupported = () =>
  !!detectorCtor() && !!navigator.mediaDevices?.getUserMedia && window.isSecureContext

/**
 * 从二维码内容里取配对码。
 *
 * `herdr-web pair` 印的码里装的是整条链接（`http://host:port/?pair=XXXXXXXX`）——
 * 相机 App 扫它会直接打开页面。这里是在页面内扫，所以只取出 `pair=` 那一段，走
 * 已有的 POST /auth/pair，不用再跳一次。顺手也认「码本身」，万一以后印的是纯码。
 */
export function pairCodeOf(raw: string): string | null {
  const norm = (v: string) => {
    const c = v.replace(/[\s-]/g, '').toUpperCase()
    return /^[0-9A-Z]{8}$/.test(c) ? c : null
  }
  const s = raw.trim()
  try {
    const p = new URL(s).searchParams.get('pair')
    if (p) return norm(p)
  } catch {
    /* 不是 URL，当纯码试 */
  }
  return norm(s)
}

export function QrScan({ onCode, onClose }: { onCode: (code: string) => void; onClose: () => void }) {
  const video = useRef<HTMLVideoElement>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    const Ctor = detectorCtor()
    if (!Ctor) return
    let stopped = false
    let stream: MediaStream | null = null

    void (async () => {
      let det: Detector
      try {
        det = new Ctor({ formats: ['qr_code'] })
        // 后摄优先：平板拿起来对着主机屏幕，用前摄等于对着自己
        stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment' } })
      } catch (e) {
        setErr('打不开相机：' + (e as Error).message)
        return
      }
      const v = video.current
      if (!v || stopped) {
        stream?.getTracks().forEach((t) => t.stop())
        return
      }
      v.srcObject = stream
      try {
        await v.play()
      } catch {
        /* 自动播放被拦就等用户点一下画面，不必报错 */
      }
      // 轮询而不是 requestVideoFrameCallback：后者 Safari 才有，而这条路上本来就没 Safari，
      // 但轮询在别的浏览器上一样够用（120ms 一帧，手拿着对准的过程比这慢得多）
      while (!stopped) {
        try {
          for (const hit of await det.detect(v)) {
            const code = pairCodeOf(hit.rawValue)
            if (code) {
              onCode(code)
              return
            }
          }
        } catch {
          /* 这一帧解不出来（模糊 / 太暗）就下一帧 */
        }
        await new Promise((r) => setTimeout(r, 120))
      }
    })()

    return () => {
      stopped = true
      stream?.getTracks().forEach((t) => t.stop())
    }
    // onCode 每次渲染都是新函数，进依赖会把相机反复重开
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div data-testid="qr-scan" className="mb-3">
      <div className="relative overflow-hidden rounded-card border border-line bg-black">
        <video
          ref={video}
          className="block max-h-[260px] w-full object-cover"
          playsInline // iOS 不加这个会全屏播放
          muted
          autoPlay
        />
        {/* 取景框：告诉人「把码放中间」，纯装饰 */}
        <div className="pointer-events-none absolute inset-0 grid place-items-center">
          <div className="size-40 rounded-xl border-2 border-brand/70" />
        </div>
      </div>
      <div className="mt-1.5 flex items-center gap-2">
        <Button size="tiny" onClick={onClose}>取消</Button>
        <span className="text-xs text-muted">
          {err || '对准机器上打出来的那个二维码，认出来就自动配对'}
        </span>
      </div>
    </div>
  )
}
