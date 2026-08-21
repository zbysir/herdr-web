package admin

import "net/http"

// 页面是一份自带的 HTML，不进前端那套构建。两个理由：
//   - 它得在「前端产物还没 build」的时候就能用（装机第一步就是配证书）；
//   - 也得在「证书坏了」的时候能用 —— 那正是你来这个页面的原因。
func page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

const html = `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>herdr-web 管理</title>
<style>
:root{--bg:#1b1d23;--bar:#23262e;--fg:#d7dae0;--muted:#868e9c;--line:#333843;
      --accent:#61afef;--ok:#98c379;--bad:#e06c75;--warn:#e5c07b}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.6 ui-sans-serif,system-ui,"PingFang SC",sans-serif}
.wrap{max-width:820px;margin:0 auto;padding:24px 18px 60px}
h1{font-size:17px;margin:0 0 2px}
.sub{color:var(--muted);font-size:12.5px;margin-bottom:22px}
section{background:var(--bar);border:1px solid var(--line);border-radius:10px;padding:14px 16px;margin-bottom:14px}
h2{font-size:13px;margin:0 0 10px;letter-spacing:.3px}
.row{display:flex;gap:10px;align-items:baseline;padding:3px 0;font-size:13px}
.k{color:var(--muted);min-width:96px;flex-shrink:0}
.v{word-break:break-all}
code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}
pre{background:var(--bg);border:1px solid var(--line);border-radius:7px;padding:10px;overflow:auto;margin:8px 0 0}
button{font:inherit;font-size:12.5px;padding:5px 11px;border-radius:7px;border:1px solid var(--line);
       background:rgba(215,218,224,.07);color:var(--fg);cursor:pointer}
button:hover{background:rgba(215,218,224,.13)}
button.p{background:var(--accent);border-color:transparent;color:#fff}
button.d{color:var(--bad)}
button:disabled{opacity:.45;cursor:default}
.btns{display:flex;flex-wrap:wrap;gap:8px;margin-top:12px}
.ok{color:var(--ok)}.bad{color:var(--bad)}.warn{color:var(--warn)}.muted{color:var(--muted)}
.accent{color:var(--accent)}
ul{list-style:none;margin:0;padding:0}
li{display:flex;gap:10px;align-items:center;padding:7px 0;border-bottom:1px solid rgba(51,56,67,.6)}
li:last-child{border-bottom:0}
li .grow{flex:1;min-width:0}
li .t{font-size:12px;color:var(--muted)}
select{font:inherit;font-size:12.5px;background:var(--bg);color:var(--fg);
       border:1px solid var(--line);border-radius:7px;padding:5px 8px}
.msg{margin-top:10px;font-size:12.5px;white-space:pre-wrap}
a{color:var(--accent)}
</style>
<div class=wrap>
  <h1>herdr-web 管理</h1>
  <div class=sub>这个页面只监听 127.0.0.1，公网上不存在 —— 所以它不需要登录。</div>
  <div id=app class=muted>读取中…</div>
</div>
<script>
const H = {'content-type':'application/json','X-Herdr-Admin':'1'}
const $ = (h) => { const d=document.createElement('div'); d.innerHTML=h.trim(); return d.firstChild }
const esc = (s) => String(s??'').replace(/[<>&"]/g, c => ({'<':'&lt;','>':'&gt;','&':'&amp;','"':'&quot;'}[c]))
const ago = (iso) => { if(!iso||iso.startsWith('0001'))return '—'
  const d=(Date.now()-new Date(iso))/1000
  return d<60?'刚刚':d<3600?~~(d/60)+' 分钟前':d<86400?~~(d/3600)+' 小时前':~~(d/86400)+' 天前' }

let S = null
async function load(){ S = await (await fetch('/api/state')).json(); render() }
async function post(u,b){ const r=await fetch(u,{method:'POST',headers:H,body:b?JSON.stringify(b):null}); return r.json() }
async function del(u){ const r=await fetch(u,{method:'DELETE',headers:H}); return r.json() }

function certSection(){
  const c=S.cert, a=S.lastAttempt
  let state, cls
  if(!c.have){ state='没有证书'; cls='bad' }
  else if(c.daysLeft<0){ state='已过期'; cls='bad' }
  else if(c.daysLeft<15){ state=c.daysLeft+' 天后到期'; cls='warn' }
  else { state=c.daysLeft+' 天后到期'; cls='ok' }
  let trust=''
  if(c.selfSigned) trust=' · <span class=warn>自签，浏览器会警告</span>'
  else if(c.staging) trust=' · <span class=warn>测试环境证书，浏览器不认</span>'

  const rows = c.have ? [
    ['域名', esc((c.domains||[]).join(' ')||c.subject)],
    ['签发者', esc(c.issuer)+trust],
    ['状态', '<span class="'+cls+'">'+state+'</span> · '+esc(c.notAfter.slice(0,10))],
    ['文件', '<code>'+esc(c.file)+'</code>'],
  ] : [['状态','<span class=bad>'+esc(c.err||'没有证书')+'</span>']]

  const acme = S.cfg.acmeDNS
  const btns = acme
    ? '<button class=p data-act=renew>立刻续期（正式）</button>'+
      '<button data-act=staging>用 staging 试一次</button>'
    : '<div class="msg muted">没配 <code>HERDR_WEB_ACME_DNS</code>，证书不是这个进程签的，'+
      '所以这儿没法续。下面「配 DNS」那节有怎么开。</div>'

  const last = a && a.at && !a.at.startsWith('0001')
    ? '<div class=msg>上次'+(a.staging?'（staging）':'')+'：'+ago(a.at)+' · '+
      (a.err?'<span class=bad>失败</span>\n'+esc(a.err):(a.renewed?'<span class=ok>签发成功</span>':'现有证书还够用，没动'))+'</div>'
    : ''

  return '<section><h2>证书</h2>'+
    rows.map(([k,v])=>'<div class=row><div class=k>'+k+'</div><div class=v>'+v+'</div></div>').join('')+
    '<div class=btns>'+btns+'</div>'+last+'</section>'
}

function dnsSection(){
  const cur = S.cfg.acmeDNS
  const opts = S.providers.map(p=>'<option value="'+p.name+'"'+(p.name===cur?' selected':'')+'>'+esc(p.label)+'</option>').join('')
  return '<section><h2>配 DNS（生成 .env 片段）</h2>'+
    '<div class=row><div class=k>服务商</div><div class=v><select id=prov>'+opts+'</select></div></div>'+
    '<div id=provInfo></div>'+
    '<div class="msg muted">凭据我们不代管 —— 你自己粘进 <code>.env</code>。这样你还可以把它放在'+
    '更好的地方（比如 macOS 的 Keychain：<code>CLOUDFLARE_DNS_API_TOKEN=$(security find-generic-password -s cf-dns -w)</code>），'+
    '而我们一旦接管就只能存明文。详细说明见仓库里的 <code>DNS.md</code>。</div></section>'
}

function provInfo(){
  const p = S.providers.find(x=>x.name===document.getElementById('prov').value)
  if(!p) return
  const snippet = 'HERDR_WEB_HOSTNAME='+((S.cfg.hostnames||[])[0]||'herdr.example.com')+'\n'+
    'HERDR_WEB_ACME_DNS='+p.name+'\n'+
    'HERDR_WEB_ACME_STAGING=1        # 跑通了再删掉这行\n'+
    p.vars.map(v=>v+'=').join('\n')
  document.getElementById('provInfo').innerHTML =
    '<div class=row><div class=k>去哪儿建</div><div class=v><a href="'+esc(p.console)+'" target=_blank rel=noreferrer>'+esc(p.console)+'</a></div></div>'+
    '<div class=row><div class=k>给什么权限</div><div class=v>'+esc(p.perm)+'</div></div>'+
    '<pre id=snip>'+esc(snippet)+'</pre>'+
    '<div class=btns><button data-act=copy>复制</button></div>'
}

function listSection(title, items, render, empty){
  return '<section><h2>'+title+'</h2>'+
    (items.length? '<ul>'+items.map(render).join('')+'</ul>' : '<div class="msg muted">'+empty+'</div>')+
    (title==='已配对的设备'
      ? '<div class=btns><button class=p data-act=pair>出一个配对码</button>'+
        (items.length?'<button class=d data-act=revokeall>全部踢掉</button>':'')+'</div><div id=pairOut></div>'
      : '')+'</section>'
}

// 有新版本就在最上面横一条。放最上面是因为这是**唯一**会主动出现的待办 ——
// 其余几块（证书、设备）都是你自己来查的时候才看。
function versionSection(){
  const v = S.version || {}
  if(!v.outdated) return ''
  return '<section style="border-color:var(--accent)">'+
    '<h2 class=accent>⬆️ 有新版本 '+esc(v.latest)+'</h2>'+
    '<div class=row><div class=k>当前</div><div class=v>'+esc(v.current||'?')+'</div></div>'+
    '<div class=row><div class=k>怎么升</div><div class=v><code>'+esc(v.how||'herdr-web update')+'</code></div></div>'+
    (v.url?'<div class=row><div class=k>更新说明</div><div class=v><a href="'+esc(v.url)+'" target=_blank rel=noreferrer>'+esc(v.url)+'</a></div></div>':'')+
    '<div class="msg muted">升完要重启才生效（<code>herdr-web service restart</code>），'+
    '重启会掐掉所有正在用的终端会话。</div>'+
  '</section>'
}

// 「当前配置」里那一行版本。没查到 / 查失败都要说清楚，空白会让人以为是自己看漏了。
function versionRow(){
  const v = S.version || {}
  let s = v.current || '?'
  if(v.outdated) s += '  →  ' + v.latest + ' 可用'
  else if(v.latest) s += '（已是最新）'
  else if(v.err) s += '（查更新失败：' + v.err + '）'
  else s += '（没查更新）'
  return s
}

function render(){
  const c = S.cfg
  const warn = []
  if(c.exposed) warn.push('这个口声明了能从公网碰到（EXPOSED=1）')
  if(S.locked) warn.push('<span class=bad>限速熔断中：新设备配不进来</span> <button data-act=unlock>解开</button>')

  document.getElementById('app').innerHTML =
    versionSection() +
    certSection() +
    dnsSection() +
    listSection('已配对的设备', S.devices, d =>
      '<li><div class=grow><div>'+esc(d.label)+'</div><div class=t>'+ago(d.lastSeen)+' · '+esc(d.lastIp||'—')+'</div></div>'+
      '<button class=d data-act=revoke data-id="'+esc(d.id)+'">踢掉</button></li>',
      '还没有设备。点下面出一个配对码，在手机上打开那个链接。') +
    listSection('passkey', S.passkeys, p =>
      '<li><div class=grow><div>'+esc(p.label)+'</div><div class=t>最后用于 '+ago(p.lastUsed)+'</div></div>'+
      '<button class=d data-act=delpk data-id="'+esc(p.id)+'">删除</button></li>',
      '还没有。在网页端（不是这里）的设置 → 设备里添加 —— 注册要在你平时用的那个浏览器上做。') +
    '<section><h2>当前配置</h2>'+
      [['版本',versionRow()],['域名',(c.hostnames||[]).join(' ')||'—'],['访问地址',c.publicURL||'—'],
       ['TLS 档位',c.tlsMode],['passkey 域名',c.rpid||'不可用（要用域名访问）'],
       ['重验间隔',c.reauthHours?c.reauthHours+' 小时':'关'],
       ['凭据有效期',c.ttlDays?c.ttlDays+' 天（滑动）':'永不过期'],
       ['数据目录',c.dataDir]]
      .map(([k,v])=>'<div class=row><div class=k>'+k+'</div><div class=v>'+esc(v)+'</div></div>').join('')+
      (warn.length?'<div class=msg>⚠️ '+warn.join('<br>⚠️ ')+'</div>':'')+
    '</section>'
  document.getElementById('prov').onchange = provInfo
  provInfo()
}

document.addEventListener('click', async (e) => {
  const b = e.target.closest('button[data-act]'); if(!b) return
  const act = b.dataset.act, id = b.dataset.id
  const busy = (t) => { b.disabled=true; b.dataset.old=b.textContent; b.textContent=t }
  const done = () => { b.disabled=false; if(b.dataset.old) b.textContent=b.dataset.old }
  try{
    if(act==='renew' || act==='staging'){
      busy('签发中…（要等 DNS 传播，几十秒正常）')
      const r = await post('/api/cert/renew', {staging: act==='staging'})
      done(); await load()
      if(r.err) alert('失败：\n'+r.err)
    } else if(act==='pair'){
      const r = await post('/api/pair')
      document.getElementById('pairOut').innerHTML =
        '<pre>'+esc(r.code)+'\n'+esc(r.url||'')+'</pre>'+
        '<div class="msg muted">5 分钟过期、用一次就废。</div>'
    } else if(act==='revoke'){ await del('/api/devices/'+id); await load() }
    else if(act==='revokeall'){ if(confirm('把所有设备都踢掉？')){ await del('/api/devices'); await load() } }
    else if(act==='delpk'){ await del('/api/passkeys/'+id); await load() }
    else if(act==='unlock'){ await post('/api/unlock'); await load() }
    else if(act==='copy'){
      const t=document.getElementById('snip').textContent
      try{ await navigator.clipboard.writeText(t) }catch{ }
      b.textContent='已复制'; setTimeout(()=>b.textContent='复制',1200)
    }
  }catch(err){ done(); alert(err.message||String(err)) }
})

load().catch(e => document.getElementById('app').innerHTML =
  '<span class=bad>读不到状态：'+esc(e.message)+'</span>')
</script>
`
