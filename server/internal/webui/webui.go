// Package webui 把构建好的前端（web/dist）嵌进二进制，由同一个进程提供界面与接口。
//
// 构建顺序：先 `make web`（pnpm build 后复制到 build/），再 go build。
// 没有构建前端时 build/ 里只有 .gitignore，Handler 返回一个提示页，接口照常可用。
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:build
var embedded embed.FS

// 嵌入文件没有修改时间，用进程启动时间做 Last-Modified。
var startedAt = time.Now()

// IndexHTML 返回嵌入的 index.html（和 Handler 下发的字节完全相同）。
// 没有嵌入前端时 ok=false；CSP 中间件用它算内联脚本哈希，见 csp.go。
func IndexHTML() ([]byte, bool) {
	dist, err := fs.Sub(embedded, "build")
	if err != nil {
		return nil, false
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, false
	}
	return index, true
}

// Handler 提供静态文件；不是文件的路径一律回退到 index.html，交给前端路由。
func Handler() http.Handler {
	dist, _ := fs.Sub(embedded, "build")
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("这个二进制没有嵌入前端：在仓库根目录执行 make build 重新构建。接口 /api、/v1、/npm 不受影响。\n"))
		})
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(dist, name); err == nil && !st.IsDir() {
				// Vite 产物文件名带内容哈希，可以长期缓存
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			// 带扩展名却找不到的（旧版本的 js、图片），返回 404 而不是 index.html
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", startedAt, bytes.NewReader(index))
	})
}
