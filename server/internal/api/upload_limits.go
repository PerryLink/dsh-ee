package api

// 上传/转换入口的请求体读取时限与名额 429 的统一写法。
//
// 这些入口（网页上传、npm publish、预检、知识库上传/重传）的共同点是：请求体是客户端
// 可控的字节流，服务端要先把整包读进来（或解到临时文件）才能处理。名额（packageUploadSlots /
// kbConvertSlots）不能从读请求体之前就占上：一个只发一半 body 的慢速客户端能把名额无限期
// 占住，让其他所有人的发布/预检/上传全部 429。
//
// 全局 http.Server.ReadTimeout 不能用来兜底（会误伤 /mcp、/v1 的流式接口），所以在几条
// 上传路由的 handler 里用 http.ResponseController.SetReadDeadline 只给请求体设总时限：
// 到点后 r.Body 的读取返回超时错误，服务端回 408，连接被关掉，名额与 goroutine 都释放。

import (
	"errors"
	"net"
	"net/http"
	"os"
	"time"
)

const (
	// packageBodyReadTimeout 是网页上传/预检与 npm publish 读请求体的总时限。请求体上限
	// 约 137 MB（100 MB tarball 的 base64 + 元数据），按 ~1.2 MB/s 的下限带宽留 2 分钟。
	packageBodyReadTimeout = 2 * time.Minute
	// kbBodyReadTimeout 是知识库上传/重传读请求体的总时限：multipart 总量上限 200 MB，
	// 按 ~0.7 MB/s 的下限带宽留 5 分钟。
	kbBodyReadTimeout = 5 * time.Minute
	// 名额占满时给客户端的 Retry-After（秒）。名额只保护正在进行的转换/入库，很快会释放。
	busyRetryAfter = "30"
)

// bodyReadTimeout 返回这次请求体读取用的时限：测试可以注入很短的超时验证超时路径，
// 正式部署用传入的默认值。
func (s *Server) bodyReadTimeout(def time.Duration) time.Duration {
	if s.opts.UploadReadTimeout > 0 {
		return s.opts.UploadReadTimeout
	}
	return def
}

// beginBodyReadDeadline 给这次请求体的读取设一个总时限，返回的函数在读完 body 后调用，
// 解除时限（避免后续处理超过时限后连接被误关）。ResponseWriter 不支持 SetReadDeadline
// 时（例如 httptest.ResponseRecorder）直接返回空函数，不影响正常处理。
func beginBodyReadDeadline(w http.ResponseWriter, timeout time.Duration) (done func()) {
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return func() {}
	}
	return func() { _ = rc.SetReadDeadline(time.Time{}) }
}

// bodyReadTimedOut 判断读请求体失败是否因为 beginBodyReadDeadline 设的读超时。
// 业务处理里的 context 超时是另一回事（不是 net.Error），不会被误判。
func bodyReadTimedOut(err error) bool {
	var ne net.Error
	return errors.Is(err, os.ErrDeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}

// writeUploadBusy 写「并发名额占满」的 429，带 Retry-After：名额释放得快，让客户端
// 等一会儿再来，而不是立刻重试继续挤。
func writeUploadBusy(w http.ResponseWriter, msg string) {
	w.Header().Set("Retry-After", busyRetryAfter)
	writeErr(w, http.StatusTooManyRequests, msg)
}
