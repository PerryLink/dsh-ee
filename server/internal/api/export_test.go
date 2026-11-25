package api

import (
	"context"
	"time"
)

// RunElevationExpiry 让黑盒测试单独跑管理模式到期监控（RunBackground 还会起一堆别的后台任务）。
func (s *Server) RunElevationExpiry(ctx context.Context) { s.runElevationExpiry(ctx) }

// SetPackageUploadReadDeadlineForTest 把包上传的请求体读时限改成 d，返回恢复函数。
// 必须在启动测试服务端之前调用：服务端 goroutine 之后才创建，能安全读到新值。
func SetPackageUploadReadDeadlineForTest(d time.Duration) (restore func()) {
	old := packageUploadReadDeadline
	packageUploadReadDeadline = d
	return func() { packageUploadReadDeadline = old }
}

// OccupyPackageUploadSlotsForTest 占住 n 个包上传名额，返回释放函数。
// 黑盒测试拿不到 beginPackageUpload 的 release，用它确定性地模拟慢速上传占满闸门。
func OccupyPackageUploadSlotsForTest(n int) (release func()) {
	releases := make([]func(), 0, n)
	for i := 0; i < n; i++ {
		r, ok := beginPackageUpload()
		if !ok {
			for _, done := range releases {
				done()
			}
			return func() {}
		}
		releases = append(releases, r)
	}
	return func() {
		for _, done := range releases {
			done()
		}
	}
}
