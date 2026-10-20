package serve

import (
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// wantsHTML reports whether the request prefers an HTML answer to a JSON one,
// read from the Accept header's media ranges. A wildcard does not count: a
// browser names text/html, while curl and fetch clients send */* or JSON.
func wantsHTML(h http.Header) bool {
	html, json := -1.0, -1.0
	for part := range strings.SplitSeq(h.Get("Accept"), ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		q := 1.0
		if raw, ok := params["q"]; ok {
			if v, err := strconv.ParseFloat(raw, 64); err == nil {
				q = v
			}
		}
		switch media {
		case "text/html":
			html = max(html, q)
		case "application/json":
			json = max(json, q)
		}
	}
	return html > 0 && html >= json
}

// pageMissing is what a browser reads when this server has no built interface:
// what it is, and the two ways to get one. It names nothing from the request.
const pageMissing = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Reasonix server: no web page</title>
<style>body{font:16px/1.55 system-ui,sans-serif;max-width:42rem;margin:12vh auto;padding:0 1.25rem;color:#222;background:#fbfbf9}
code,pre{font:14px ui-monospace,Menlo,Consolas,monospace;background:#efefe9;border-radius:4px}code{padding:.1em .35em}pre{padding:.7em .9em;overflow:auto}
h1{font-size:1.35rem}h2{font-size:1rem;margin-top:2rem}@media(prefers-color-scheme:dark){body{color:#e7e8e3;background:#181817}code,pre{background:#262624}}</style></head>
<body>
<h1>This Reasonix server is running, but it has no web page</h1>
<p>The standalone <code>reasonix</code> program serves the API only. The web interface ships with the Reasonix Studio desktop app, so a browser opened here finds nothing to draw.</p>
<h2>Use the interface</h2>
<p>Open the Reasonix Studio app, or serve its page from this command. Install Studio once, then start the server with the folder that holds Studio's <code>index.html</code>:</p>
<pre>reasonix serve --page "&lt;Studio install folder&gt;/resources/frontend-next/dist"</pre>
<p>On macOS the folder sits inside the app bundle, so the path is <code>Reasonix Studio.app/Contents/Resources/frontend-next/dist</code>. Open the address it prints again afterwards.</p>
<h2>Talk to the API</h2>
<p>A client that asks for JSON gets a JSON answer with the code <code>page.not_built</code>; nothing about the API is missing.</p>
<hr>
<h1>Reasonix 服务正在运行，但没有网页界面</h1>
<p>单独的 <code>reasonix</code> 程序只提供接口。网页界面随 Reasonix Studio 桌面版一起发布，所以在浏览器里打开这个地址看不到界面。</p>
<p>请直接打开 Studio 桌面版；或者先安装一次 Studio，再用包含 Studio 的 <code>index.html</code> 的文件夹启动服务：</p>
<pre>reasonix serve --page "&lt;Studio 安装目录&gt;/resources/frontend-next/dist"</pre>
<p>macOS 上这个文件夹在应用包里面，路径是 <code>Reasonix Studio.app/Contents/Resources/frontend-next/dist</code>。之后重新打开它打印出的地址即可。要求 JSON 的客户端仍会收到带 <code>page.not_built</code> 代码的 JSON。</p>
</body></html>
`

func writePageMissing(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(pageMissing))
}
