package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/registry"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

func (s *Server) packageSourceErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "Package source or artifact not found")
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "Package source or immutable artifact already exists")
	case errors.Is(err, store.ErrIncompletePackageRevision):
		writeErr(w, http.StatusConflict, "Conan revision is incomplete; upload all files before the manifest")
	case errors.Is(err, store.ErrPackageSourceOwner):
		writeErr(w, http.StatusForbidden, "Artifact belongs to another publisher")
	case errors.Is(err, registry.ErrTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, "Artifact exceeds the 512 MiB limit")
	default:
		s.internalErr(w, r, err)
	}
}

func (s *Server) handleListPackageSources(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListPackageSources(r.Context())
	if err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreatePackageSource(w http.ResponseWriter, r *http.Request) {
	var src store.PackageSource
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&src); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid package source")
		return
	}
	if err := registry.ValidatePackageSource(&src); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.CreatePackageSource(r.Context(), src); err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	s.audit(r, auditSettingsUpdate, "package_sources/"+src.Protocol+"/"+src.Name, map[string]any{"mode": src.Mode})
	writeJSON(w, http.StatusCreated, src)
}

func (s *Server) packageSource(w http.ResponseWriter, r *http.Request, protocol, name string) (store.PackageSource, bool) {
	if (protocol != "raw" && protocol != "conan") || !registry.ValidPackageSourceName(name) {
		writeErr(w, http.StatusNotFound, "Package source not found")
		return store.PackageSource{}, false
	}
	src, err := s.store.PackageSource(r.Context(), protocol, name)
	if err != nil {
		s.packageSourceErr(w, r, err)
	}
	return src, err == nil
}

func (s *Server) handleUpdatePackageSource(w http.ResponseWriter, r *http.Request) {
	src, ok := s.packageSource(w, r, r.PathValue("protocol"), r.PathValue("source"))
	if !ok {
		return
	}
	if src.Protocol != "raw" || src.Mode != "proxy" {
		writeErr(w, http.StatusBadRequest, "Only Raw proxy upstream URLs can be changed")
		return
	}
	var body struct {
		URL string `json:"upstream_url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid package source")
		return
	}
	src.UpstreamURL = body.URL
	if err := registry.ValidatePackageSource(&src); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpdatePackageSourceUpstream(r.Context(), src.ID, src.UpstreamURL); err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	s.audit(r, auditSettingsUpdate, "package_sources/raw/"+src.Name, nil)
	writeJSON(w, http.StatusOK, src)
}

func (s *Server) handlePackageArtifacts(w http.ResponseWriter, r *http.Request) {
	src, ok := s.packageSource(w, r, r.PathValue("protocol"), r.PathValue("source"))
	if !ok {
		return
	}
	after := r.URL.Query().Get("after")
	if len(after) > 1024 {
		writeErr(w, http.StatusBadRequest, "Invalid artifact cursor")
		return
	}
	const pageSize = 50
	items, err := s.store.PackageArtifacts(r.Context(), src.ID, nil, after, pageSize+1)
	if err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	next := ""
	if len(items) > pageSize {
		items = items[:pageSize]
		next = items[len(items)-1].Path
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next": next})
}

func packageRequestPath(r *http.Request, protocol string) ([]string, bool) {
	return registry.PackagePath(strings.TrimPrefix(r.URL.EscapedPath(), "/"+protocol+"/"))
}

func packageSourcePaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped := r.URL.EscapedPath()
		for _, prefix := range []string{"/raw/", "/conan/"} {
			if rest, ok := strings.CutPrefix(escaped, prefix); ok {
				if _, valid := registry.PackagePath(rest); !valid {
					writeErr(w, http.StatusBadRequest, "Invalid package source path")
					return
				}
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) packagePrincipal(w http.ResponseWriter, r *http.Request, token string, publish, login bool) *Principal {
	if strings.HasPrefix(token, "ee_at_") {
		p, err := s.resolveBearer(r.Context(), token, s.clientIP(r), r.Header.Get("X-DshEE-Client"), r.Header.Get("X-DshEE-Host"))
		if err == nil {
			canRead := p.Access.Has(rbac.RegistryUse) && p.Scopes.Has(rbac.ScopeRegistryRead)
			canPublish := p.Access.Has(rbac.RegistryPublish) && p.Scopes.Has(rbac.ScopeRegistryPublish)
			if (!publish && canRead) || (publish && canPublish) || (login && canPublish) {
				return p
			}
			writeErr(w, http.StatusForbidden, "Token has insufficient registry permissions")
			return nil
		}
		var be bearerError
		if !errors.As(err, &be) {
			s.internalErr(w, r, err)
			return nil
		}
	}
	scheme := "Bearer"
	if login {
		scheme = "Basic"
	}
	w.Header().Set("WWW-Authenticate", scheme+` realm="dsh-ee"`)
	writeErr(w, http.StatusUnauthorized, "A valid dsh-ee access token is required")
	return nil
}

func packageBearer(r *http.Request) string {
	scheme, value, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(value)
	}
	return ""
}

func packageMethod(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for _, method := range allowed {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeErr(w, http.StatusMethodNotAllowed, "Unsupported package source method")
	return false
}

func (s *Server) handleRawSource(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	segments, valid := packageRequestPath(r, "raw")
	if !valid || len(segments) < 2 {
		writeErr(w, http.StatusBadRequest, "Invalid Raw artifact path")
		return
	}
	src, ok := s.packageSource(w, r, "raw", segments[0])
	if !ok {
		return
	}
	if !packageMethod(w, r, http.MethodGet, http.MethodHead, http.MethodPut) {
		return
	}
	if src.Mode == "proxy" && r.Method == http.MethodPut {
		packageMethod(w, r, http.MethodGet, http.MethodHead)
		return
	}
	if s.demoSiteNpmWrite(w, r) {
		return
	}
	p := s.packagePrincipal(w, r, packageBearer(r), r.Method == http.MethodPut, false)
	if p == nil {
		return
	}
	name := strings.Join(segments[1:], "/")
	if src.Mode == "proxy" {
		for _, segment := range segments[1:] {
			if strings.Contains(segment, "%") {
				writeErr(w, http.StatusBadRequest, "Invalid Raw proxy path")
				return
			}
		}
		s.proxyPackage(w, r, src, segments[1:], true)
	} else if r.Method == http.MethodPut {
		s.putPackageFile(w, r, p, src, name, name, registry.ConanPath{}, true)
	} else {
		s.getPackageFile(w, r, src, name)
	}
}

func packageAttachment(w http.ResponseWriter, file string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(file)}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) getPackageFile(w http.ResponseWriter, r *http.Request, src store.PackageSource, name string) {
	if s.registry == nil {
		writeErr(w, http.StatusServiceUnavailable, "Package storage is not initialized")
		return
	}
	f, err := s.store.PackageArtifact(r.Context(), src.ID, name)
	if err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	blob, err := s.registry.Blobs().Open(f.BlobKey)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	defer blob.Close()
	packageAttachment(w, name)
	w.Header().Set("ETag", `"`+f.BlobKey+`"`)
	http.ServeContent(w, r, name, f.UpdatedAt, blob)
}

func (s *Server) putPackageFile(w http.ResponseWriter, r *http.Request, p *Principal, src store.PackageSource, name, namespace string, conan registry.ConanPath, mutable bool) {
	if s.registry == nil {
		writeErr(w, http.StatusServiceUnavailable, "Package storage is not initialized")
		return
	}
	limit := registry.PackageFileLimit
	if conan.File == "conanmanifest.txt" {
		limit = 4 << 20
	}
	if r.ContentLength > limit {
		writeErr(w, http.StatusRequestEntityTooLarge, "Artifact exceeds the upload size limit")
		return
	}
	digest, err := s.registry.Blobs().Put(r.Body, limit)
	if err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	if expected := r.Header.Get("X-Checksum-Sha1"); expected != "" && !strings.EqualFold(expected, digest.Shasum) {
		writeErr(w, http.StatusBadRequest, "Artifact checksum mismatch")
		return
	}
	var required, archives []string
	if conan.File == "conanmanifest.txt" {
		blob, err := s.registry.Blobs().Open(digest.Key)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		required, archives, err = registry.ConanPublicationFiles(blob, conan.Binary)
		_ = blob.Close()
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	err = s.store.SavePackageSourceFile(r.Context(), src.ID, namespace, conan.Revision, conan.Parent,
		store.PackageSourceFile{Path: name, BlobKey: digest.Key, Size: digest.Size, UploaderID: p.User.ID}, mutable, conan.Archive, required, archives)
	if err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	s.auditToken(r, p, auditPackagePublish, src.Protocol+"/"+src.Name+"/"+name, map[string]any{"size": digest.Size})
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) proxyPackage(w http.ResponseWriter, r *http.Request, src store.PackageSource, segments []string, attachment bool) {
	if s.opts.DemoSite != nil {
		demoSiteRefuse(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	resp, err := s.packageProxy.Fetch(ctx, r.Method, src.UpstreamURL, segments, r.URL.RawQuery, r.Header)
	if err != nil {
		s.log.Warn("package proxy request failed", "protocol", src.Protocol, "source", src.Name)
		writeErr(w, http.StatusBadGateway, "Package upstream request failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		status := resp.StatusCode
		if status != http.StatusNotFound {
			status = http.StatusBadGateway
		}
		writeErr(w, status, "Package upstream did not return an artifact")
		return
	}
	if resp.ContentLength > registry.PackageFileLimit {
		s.packageSourceErr(w, r, registry.ErrTooLarge)
		return
	}
	if attachment {
		packageAttachment(w, segments[len(segments)-1])
	} else {
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	}
	for _, key := range []string{"Accept-Ranges", "Content-Encoding", "Content-Range", "ETag", "Last-Modified", "X-Conan-Server-Capabilities", "X-Conan-Server-Version", "X-Checksum-Sha1", "X-Checksum-Sha256", "Content-Digest"} {
		if v := resp.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, registry.PackageFileLimit))
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	if n == registry.PackageFileLimit && resp.ContentLength < 0 {
		var extra [1]byte
		if _, err := io.ReadFull(resp.Body, extra[:]); err != io.EOF {
			panic(http.ErrAbortHandler)
		}
	}
}
