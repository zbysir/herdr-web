"""herdr socket API 极简客户端。注意：服务端一个连接只处理一个请求，不支持 pipeline。"""
import json, os, socket

SOCK = os.environ.get("HERDR_SOCKET_PATH",
                      os.path.expanduser("~/.config/herdr/herdr.sock"))

def call(method, params, timeout=10, _id="api"):
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(timeout)
    s.connect(SOCK)
    try:
        s.sendall((json.dumps({"id": _id, "method": method, "params": params}) + "\n").encode())
        buf = b""
        while b"\n" not in buf:
            c = s.recv(65536)
            if not c:
                raise RuntimeError("连接被关闭且无响应")
            buf += c
        return json.loads(buf.split(b"\n", 1)[0])
    finally:
        s.close()

def subscribe(subscriptions, timeout=None):
    """生成器：持续 yield 推送事件。连接保持打开。"""
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    if timeout: s.settimeout(timeout)
    s.connect(SOCK)
    s.sendall((json.dumps({"id": "sub", "method": "events.subscribe",
                           "params": {"subscriptions": subscriptions}}) + "\n").encode())
    buf = b""
    while True:
        c = s.recv(65536)
        if not c: return
        buf += c
        while b"\n" in buf:
            line, buf = buf.split(b"\n", 1)
            if line.strip():
                yield json.loads(line)
