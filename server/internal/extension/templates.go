package extension

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// `dsh-ee extension init` 用的模板，编译在二进制里。
//
//go:embed templates
var templateFiles embed.FS

// Template 返回一个新扩展的初始文件（相对路径 → 内容）。kind 是 declarative 或 service。
func Template(kind, id string) (map[string][]byte, error) {
	if !idRe.MatchString(id) {
		return nil, fmt.Errorf("id %q 不对：要写成 作者/名字，只用小写字母、数字、连字符，例如 acme/oa", id)
	}
	if kind != KindDeclarative && kind != KindService {
		return nil, fmt.Errorf("--kind 只能是 declarative 或 service（端侧扩展见 extensions/sdk-local）")
	}
	_, name, _ := strings.Cut(id, "/")
	out := map[string][]byte{}
	root := "templates/" + kind
	err := fs.WalkDir(templateFiles, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := templateFiles.ReadFile(path)
		if err != nil {
			return err
		}
		text := strings.NewReplacer("__ID__", id, "__NAME__", name).Replace(string(raw))
		rel := strings.TrimPrefix(path, root+"/")
		// go.mod、*.go 在模板目录里带 .tmpl 后缀，免得被当成这个模块的一部分
		rel = strings.TrimSuffix(rel, ".tmpl")
		out[rel] = []byte(text)
		return nil
	})
	return out, err
}
