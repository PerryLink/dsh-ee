package registry

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var conanComponent = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_+.-]{0,50}$`)
var conanDigest = regexp.MustCompile(`^[0-9a-f]{1,64}$`)
var conanArchive = regexp.MustCompile(`^conan_(export|sources|package)\.(tgz|txz|tzst|tar\.gz|tar\.xz|tar\.zst)$`)

type ConanPath struct {
	Operation string
	Parent    string
	Revision  string
	File      string
	Archive   string
	Namespace string
	Binary    bool
}

func ParseConanPath(s []string) (v ConanPath, ok bool) {
	switch strings.Join(s, "/") {
	case "v1/ping":
		return ConanPath{Operation: "ping"}, true
	case "v2/users/authenticate":
		return ConanPath{Operation: "authenticate"}, true
	case "v2/users/check_credentials":
		return ConanPath{Operation: "credentials"}, true
	case "v2/conans/search":
		return ConanPath{Operation: "search"}, true
	}
	if len(s) < 7 || s[0] != "v2" || s[1] != "conans" {
		return v, false
	}
	for _, value := range s[2:6] {
		if !conanComponent.MatchString(value) {
			return v, false
		}
	}
	v.Parent = strings.Join(s[1:6], "/")
	v.Namespace = strings.Join([]string{s[2], s[4], s[5]}, "/")
	rest := s[6:]
	if len(rest) >= 4 && rest[0] == "revisions" && conanDigest.MatchString(rest[1]) && rest[2] == "packages" && conanDigest.MatchString(rest[3]) {
		v.Parent += "/" + strings.Join(rest[:4], "/")
		v.Binary, rest = true, rest[4:]
	}
	if len(rest) == 1 && (rest[0] == "latest" || rest[0] == "revisions" || rest[0] == "search" && !v.Binary) {
		v.Operation = rest[0]
		if rest[0] == "search" {
			v.Operation = "packages"
		}
		return v, true
	}
	if len(rest) < 3 || rest[0] != "revisions" || !conanDigest.MatchString(rest[1]) {
		return v, false
	}
	v.Revision = v.Parent + "/revisions/" + rest[1]
	if !v.Binary && len(rest) == 3 && rest[2] == "search" {
		v.Operation = "packages"
		return v, true
	}
	if rest[2] != "files" {
		return v, false
	}
	v.Operation = "files"
	if len(rest) == 3 {
		return v, true
	}
	v.Operation, v.File = "file", strings.Join(rest[3:], "/")
	if strings.HasPrefix(v.File, "metadata/") {
		return v, true
	}
	if conanArchive.MatchString(v.File) {
		v.Archive, _, _ = strings.Cut(v.File, ".")
		return v, v.Binary == (v.Archive == "conan_package")
	}
	return v, v.File == "conanmanifest.txt" || v.Binary && v.File == "conaninfo.txt" || !v.Binary && v.File == "conanfile.py"
}

func ConanPublicationFiles(r io.Reader, binary bool) (required, archives []string, err error) {
	reader := bufio.NewScanner(r)
	if !reader.Scan() {
		return nil, nil, fmt.Errorf("invalid Conan manifest")
	}
	if _, err := strconv.ParseInt(strings.TrimSpace(reader.Text()), 10, 64); err != nil {
		return nil, nil, fmt.Errorf("invalid Conan manifest")
	}
	var hasExport, hasSources bool
	for reader.Scan() {
		line := strings.TrimSuffix(reader.Text(), "\r")
		if line == "" {
			continue
		}
		sep := strings.LastIndex(line, ": ")
		if sep <= 0 || len(line[sep+2:]) != 32 || !conanDigest.MatchString(line[sep+2:]) {
			return nil, nil, fmt.Errorf("invalid Conan manifest")
		}
		name := line[:sep]
		if strings.HasPrefix(name, "export_source/") {
			hasSources = true
		} else if name != "conanfile.py" {
			hasExport = true
		}
	}
	if err := reader.Err(); err != nil {
		return nil, nil, fmt.Errorf("invalid Conan manifest: %w", err)
	}
	if binary {
		return []string{"conaninfo.txt"}, []string{"conan_package"}, nil
	}
	if hasExport {
		archives = append(archives, "conan_export")
	}
	if hasSources {
		archives = append(archives, "conan_sources")
	}
	return []string{"conanfile.py"}, archives, nil
}

func ConanReference(parent string) string {
	parts := strings.Split(parent, "/")
	if len(parts) != 5 || parts[0] != "conans" {
		return ""
	}
	ref := parts[1] + "/" + parts[2]
	if parts[3] != "_" || parts[4] != "_" {
		ref += "@" + parts[3] + "/" + parts[4]
	}
	return ref
}

func MatchConanReference(pattern, ref string, ignoreCase bool) bool {
	if pattern == "" {
		return true
	}
	if strings.HasSuffix(pattern, "@") {
		if strings.Contains(ref, "@") {
			return false
		}
		pattern = strings.TrimSuffix(pattern, "@")
	}
	if !strings.Contains(pattern, "@") {
		ref, _, _ = strings.Cut(ref, "@")
	}
	if ignoreCase {
		pattern, ref = strings.ToLower(pattern), strings.ToLower(ref)
	}
	// Conan wildcards can span a slash.
	matched, _ := path.Match(strings.ReplaceAll(pattern, "/", "\x00"), strings.ReplaceAll(ref, "/", "\x00"))
	return matched
}
