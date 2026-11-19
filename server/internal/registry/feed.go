package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// FeedPlugin 是插件源目录（DSH 索引 format 1，dshfind 或第三方源）里的一条，上游预载从这里挑。
type FeedPlugin struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	Repository  string `json:"repository,omitempty"`
	Category    string `json:"category,omitempty"`
	Featured    bool   `json:"featured,omitempty"`
	Stars       *int   `json:"stars,omitempty"`
	Score       *int   `json:"score,omitempty"`
	Version     string `json:"version,omitempty"`
	PublishedAt int64  `json:"published_at,omitempty"`
	// 来自哪个插件源（DshfindSource 或第三方源的名称）
	Source string `json:"source,omitempty"`
}

func (f *FeedPlugin) patch() store.CatalogPatch {
	p := store.CatalogPatch{Featured: &f.Featured, UpstreamScore: f.Score, UpstreamStars: f.Stars}
	if f.Title != "" {
		p.Title = &f.Title
	}
	if f.Description != "" {
		d := truncateRunes(f.Description, 1000)
		p.Description = &d
	}
	if f.Author != "" {
		p.Author = &f.Author
	}
	if icon := iconURL(f.Icon); icon != "" {
		p.Icon = &icon
	}
	if home := httpURL(f.Homepage); home != "" {
		p.Homepage = &home
	}
	if repo := httpURL(f.Repository); repo != "" {
		p.Repository = &repo
	}
	for _, c := range Categories {
		if c == f.Category {
			p.Category = &f.Category
		}
	}
	return p
}

type feedCache struct {
	mu      sync.Mutex
	entries map[string]*feedEntry
}

type feedEntry struct {
	mu      sync.Mutex
	at      time.Time
	plugins []FeedPlugin
}

// DshfindSource 是内置的 dshfind 插件源的显示名。
const DshfindSource = "dshfind"

// FeedResult 是一个插件源这次取目录的结果。
type FeedResult struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Count     int       `json:"count"`
	Error     string    `json:"error,omitempty"`
	FetchedAt time.Time `json:"fetched_at,omitzero"`
}

// Sources 返回当前启用的插件源：内置 dshfind（upstream.dshfindFeed）在前，其后是管理员添加的第三方源。
func (s *Service) Sources(set *store.AppSettings) []store.FeedSource {
	var out []store.FeedSource
	if set.Upstream.DshfindFeed {
		out = append(out, store.FeedSource{Name: DshfindSource, URL: s.FeedURL, Enabled: true})
	}
	for _, src := range set.Upstream.Sources {
		if src.Enabled {
			out = append(out, src)
		}
	}
	return out
}

// Feeds 并发取各插件源的目录并合并。同名插件只保留排在前面的源里的那条；
// 某个源失败只记在它自己的结果里，不影响其它源。
func (s *Service) Feeds(ctx context.Context, refresh bool, sources []store.FeedSource) ([]FeedPlugin, []FeedResult) {
	results := make([]FeedResult, len(sources))
	lists := make([][]FeedPlugin, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plugins, at, err := s.fetchFeed(ctx, src.URL, refresh)
			results[i] = FeedResult{Name: src.Name, URL: src.URL, Count: len(plugins), FetchedAt: at}
			if err != nil {
				results[i].Error = err.Error()
			}
			lists[i] = plugins
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	var out []FeedPlugin
	for i, list := range lists {
		for _, p := range list {
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			p.Source = sources[i].Name
			out = append(out, p)
		}
	}
	s.authors.set(out)
	return out, results
}

// Feed 取内置 dshfind 插件目录，内存里缓存一小时。
func (s *Service) Feed(ctx context.Context, refresh bool) ([]FeedPlugin, time.Time, error) {
	return s.fetchFeed(ctx, s.FeedURL, refresh)
}

func (s *Service) fetchFeed(ctx context.Context, url string, refresh bool) ([]FeedPlugin, time.Time, error) {
	s.feed.mu.Lock()
	if s.feed.entries == nil {
		s.feed.entries = map[string]*feedEntry{}
	}
	feed := s.feed.entries[url]
	if feed == nil {
		feed = &feedEntry{}
		s.feed.entries[url] = feed
	}
	s.feed.mu.Unlock()
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if !refresh && time.Since(feed.at) < time.Hour && feed.plugins != nil {
		return feed.plugins, feed.at, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Set("User-Agent", "dsh-ee")
	resp, err := s.up.hc.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("访问插件目录：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("插件目录返回 %d", resp.StatusCode)
	}
	var idx struct {
		Plugins []struct {
			Name        string `json:"name"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Author      string `json:"author"`
			Icon        string `json:"icon"`
			Homepage    string `json:"homepage"`
			Repository  struct {
				URL string `json:"url"`
			} `json:"repository"`
			Metadata struct {
				Category string         `json:"category"`
				Labels   []string       `json:"labels"`
				SortKeys map[string]any `json:"sortKeys"`
			} `json:"metadata"`
			Versions []struct {
				Version          string `json:"version"`
				PublishTimestamp int64  `json:"publishTimestamp"`
			} `json:"versions"`
		} `json:"plugins"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&idx); err != nil {
		return nil, time.Time{}, fmt.Errorf("插件目录格式错误：%w", err)
	}
	out := make([]FeedPlugin, 0, len(idx.Plugins))
	for _, p := range idx.Plugins {
		if !ValidName(p.Name) {
			continue
		}
		fp := FeedPlugin{Name: p.Name, Title: p.Title, Description: p.Description, Author: p.Author, Icon: p.Icon,
			Homepage: p.Homepage, Repository: p.Repository.URL, Category: p.Metadata.Category}
		for _, l := range p.Metadata.Labels {
			if l == "featured" {
				fp.Featured = true
			}
		}
		fp.Stars = intKey(p.Metadata.SortKeys["stars"])
		fp.Score = intKey(p.Metadata.SortKeys["score"])
		if len(p.Versions) > 0 {
			last := p.Versions[len(p.Versions)-1]
			fp.Version, fp.PublishedAt = last.Version, last.PublishTimestamp
		}
		out = append(out, fp)
	}
	feed.plugins, feed.at = out, time.Now()
	return out, feed.at, nil
}

func intKey(v any) *int {
	if f, ok := v.(float64); ok {
		i := int(f)
		return &i
	}
	return nil
}
