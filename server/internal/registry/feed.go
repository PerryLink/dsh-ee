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

// FeedPlugin 是 dshfind 插件目录（DSH 索引 format 1）里的一条，上游预载从这里挑。
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
	at      time.Time
	url     string
	plugins []FeedPlugin
}

// Feed 取 dshfind 插件目录，内存里缓存一小时。
func (s *Service) Feed(ctx context.Context, refresh bool) ([]FeedPlugin, time.Time, error) {
	feed := &s.feed
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if !refresh && feed.url == s.FeedURL && time.Since(feed.at) < time.Hour && feed.plugins != nil {
		return feed.plugins, feed.at, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.FeedURL, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Set("User-Agent", "dsh-ee")
	resp, err := s.up.hc.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("访问 dshfind 目录：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("dshfind 目录返回 %d", resp.StatusCode)
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
		return nil, time.Time{}, fmt.Errorf("dshfind 目录格式错误：%w", err)
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
	feed.plugins, feed.at, feed.url = out, time.Now(), s.FeedURL
	s.authors.set(out)
	return out, feed.at, nil
}

func intKey(v any) *int {
	if f, ok := v.(float64); ok {
		i := int(f)
		return &i
	}
	return nil
}
