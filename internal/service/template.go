package service

import (
	"strings"
)

// plist 生成 launchd 的 LaunchAgent。
//
// 几个不那么显然的键：
//   - ProcessType=Interactive：不写的话 macOS 会按后台任务限速（App Nap 那一套），
//     表现是终端偶发卡顿、输入迟一拍。这是个交互式服务，得声明清楚。
//   - KeepAlive=true：崩了就重起。配置写错导致起不来的时候 launchd 会按 10s 退避
//     一直重试 —— 所以 StandardErrorPath 必须有，错误只在那儿看得到。
//   - LaunchAgent 是**登录时**起，不是开机时起。要真正无人登录也起，只能用
//     /Library/LaunchDaemons 里的系统级 daemon，但那样 shell 就是 root 的，
//     这个项目不做（理由见 package 注释）。开了自动登录的机器上二者等价。
func (m *Manager) plist() string {
	var env strings.Builder
	for _, k := range m.keys() {
		env.WriteString("    <key>" + xmlEsc(k) + "</key>\n")
		env.WriteString("    <string>" + xmlEsc(m.Env[k]) + "</string>\n")
	}
	log := xmlEsc(m.LogPath())
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + Label + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlEsc(m.Exec) + `</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
` + env.String() + `  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Interactive</string>
  <key>WorkingDirectory</key>
  <string>` + xmlEsc(m.homeDir) + `</string>
  <key>StandardOutPath</key>
  <string>` + log + `</string>
  <key>StandardErrorPath</key>
  <string>` + log + `</string>
</dict>
</plist>
`
}

// unit 生成 systemd 的 user unit。
//
//   - Environment= 的值用引号包起来：不包的话带空格的值（HERDR_WEB_ONCONNECT=
//     "herdr --session work"）会被切成两个变量，静默丢一半。
//   - systemd 会对 Environment= 的值做 `$` 展开，所以 `$` 要写成 `$$`。token 里
//     出现 `$` 完全可能，这条不处理就是随机丢字符。
//   - WantedBy=default.target 是 user 级的「开机/登录就起」。真正做到「没人登录
//     也起」还要 loginctl enable-linger，见 Install。
func (m *Manager) unit() string {
	var env strings.Builder
	for _, k := range m.keys() {
		env.WriteString("Environment=\"" + k + "=" + sdEsc(m.Env[k]) + "\"\n")
	}
	log := m.LogPath()
	return `[Unit]
Description=herdr-web —— 浏览器里的 herdr 终端
Documentation=https://github.com/zbysir/herdr-web
After=network-online.target

[Service]
Type=simple
ExecStart=` + m.Exec + `
` + env.String() + `WorkingDirectory=` + m.homeDir + `
Restart=always
RestartSec=3
# 日志同时进 journal 和这个文件。文件是为了和 macOS 那边路径一致（文档只有一套说法）；
# journalctl --user -u ` + Unit + ` 一样能看。
StandardOutput=append:` + log + `
StandardError=append:` + log + `

[Install]
WantedBy=default.target
`
}

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// sdEsc 转义 systemd Environment= 双引号里的值。
func sdEsc(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", "$$")
	return r.Replace(s)
}
