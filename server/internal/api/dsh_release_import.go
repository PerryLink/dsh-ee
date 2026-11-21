package api

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

var releaseVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

func (s *Server) handleImportDshRelease(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	item := &store.DshRelease{Target: q.Get("target"), Version: strings.TrimSpace(q.Get("version")), File: q.Get("file"), ReleasedAt: time.Now(), State: store.ReleaseMirrored}
	item.Kind = releaseKind(item.File)
	ext := strings.ToLower(filepath.Ext(item.File))
	if ext == ".dmg" {
		item.Kind = "dmg"
	}
	part, err := s.releasePartPath(item)
	if err != nil || !releaseVersionPattern.MatchString(item.Version) || strings.ContainsAny(item.File, "\x00\r\n") || (ext != ".zip" && ext != ".exe" && ext != ".msi" && ext != ".dmg") {
		writeErrCode(w, 400, codeReleaseInvalid, nil, "平台、版本或安装包文件名无效")
		return
	}
	var expected []byte
	if raw := strings.TrimSpace(q.Get("sha512")); raw != "" {
		expected, err = base64.StdEncoding.DecodeString(raw)
		if err != nil || len(expected) != sha512.Size {
			expected, err = hex.DecodeString(raw)
		}
		if err != nil || len(expected) != sha512.Size {
			writeErrCode(w, 400, codeReleaseChecksum, nil, "sha512 无效或校验不一致")
			return
		}
	}
	s.releaseMu.Lock()
	if s.releaseJobs[item.Target] {
		s.releaseMu.Unlock()
		writeErrCode(w, 409, codeReleaseBusy, nil, "该平台正在下载或导入，或文件名已被其他版本使用")
		return
	}
	s.releaseJobs[item.Target] = true
	s.releaseMu.Unlock()
	defer func() { s.releaseMu.Lock(); delete(s.releaseJobs, item.Target); s.releaseMu.Unlock() }()
	ctx := r.Context()
	rows, err := s.store.ListDshReleases(ctx, item.Target)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	var old *store.DshRelease
	for _, row := range rows {
		if row.Kind == item.Kind && row.Version == item.Version {
			old = row
			item.ID = row.ID
		}
		if (row.File == item.File && (row.Version != item.Version || row.Kind != item.Kind)) || (row.Kind == item.Kind && row.Version == item.Version && row.State == store.ReleaseDownloading) {
			writeErrCode(w, 409, codeReleaseBusy, nil, "该平台正在下载或导入，或文件名已被其他版本使用")
			return
		}
	}
	set, err := s.appSettings(ctx)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if err = os.MkdirAll(filepath.Dir(part), 0700); err != nil {
		s.internalErr(w, r, err)
		return
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	defer func() { f.Close(); os.Remove(part) }()
	r.Body = http.MaxBytesReader(w, r.Body, maxDshReleaseBytes)
	h := sha512.New()
	size, err := io.Copy(io.MultiWriter(f, h), r.Body)
	if err != nil || size == 0 {
		var tooLarge *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeErrCode(w, status, codeReleaseBody, nil, "安装包为空、超过大小限制或上传中断")
		return
	}
	if expected != nil && !bytes.Equal(expected, h.Sum(nil)) {
		writeErrCode(w, 400, codeReleaseChecksum, nil, "sha512 无效或校验不一致")
		return
	}
	if err = f.Sync(); err != nil {
		s.internalErr(w, r, err)
		return
	}
	if err = f.Close(); err != nil {
		s.internalErr(w, r, err)
		return
	}
	item.Size, item.SHA512, item.LocalPath = size, base64.StdEncoding.EncodeToString(h.Sum(nil)), strings.TrimSuffix(part, ".part")
	if err = os.Rename(part, item.LocalPath); err != nil {
		s.internalErr(w, r, err)
		return
	}
	if old == nil {
		err = s.store.CreateDshRelease(ctx, item)
	} else {
		err = s.store.UpdateImportedDshReleaseMetadata(ctx, item)
	}
	if err == nil {
		err = s.store.FinishDshRelease(ctx, item.ID, size, item.SHA512, item.LocalPath)
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if old != nil && old.LocalPath != "" && old.LocalPath != item.LocalPath {
		if err = os.Remove(old.LocalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.internalErr(w, r, err)
			return
		}
	}
	if err = s.cleanupDshReleases(ctx, item.Target, set.Distribution.KeepVersions); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditDshReleaseImport, item.Target+"/"+item.File, map[string]any{"version": item.Version})
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": item.ID})
}
