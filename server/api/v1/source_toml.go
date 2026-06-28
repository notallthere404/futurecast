package v1

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// SourceTOML flat TOML shape user-authored in config.toml. Mirrors the
// pre-polymorphic Source layout for backwards-compat with existing files;
// Into() dispatches by `type` into the proper Spec.
//
// Legacy `page` entries are folded into rss with Target=link and the
// nav/title/content/timestamp arrays packed into Selectors.
type SourceTOML struct {
	Type   SourceType `toml:"type"`
	Name   string     `toml:"name"`
	URL    string     `toml:"url"`
	Active bool       `toml:"active"`
	Trust  Trust      `toml:"trust"`

	// RSS
	Target   SourceTarget `toml:"target,omitempty"`
	Limit    int          `toml:"limit,omitempty"`
	Schedule string       `toml:"schedule,omitempty"`

	// Legacy page (folded into rss with Target=link)
	Paths     []string `toml:"paths,omitempty"`
	Nav       []string `toml:"nav,omitempty"`
	Title     []string `toml:"title,omitempty"`
	Content   []string `toml:"content,omitempty"`
	Timestamp []string `toml:"timestamp,omitempty"`
}

// Into converts the TOML shape into a *Source ready for the registry,
// building the matching Spec and serialising it into SpecRaw.
func (t *SourceTOML) Into() (*Source, error) {
	src := &Source{
		Name:   t.Name,
		URL:    t.URL,
		Active: t.Active,
		Trust:  t.Trust,
	}

	switch t.Type {
	case RSSType:
		spec := &RSSSpec{
			URL:      t.URL,
			Target:   t.Target,
			Limit:    t.Limit,
			Schedule: t.Schedule,
		}
		src.Type = RSSType
		src.Spec = spec

	case "page":
		// Folded into rss with Target=link.
		sels := SelectorMap{}
		if len(t.Nav) > 0 {
			sels[Nav] = t.Nav
		}
		if len(t.Title) > 0 {
			sels[Title] = t.Title
		}
		if len(t.Content) > 0 {
			sels[Content] = t.Content
		}
		if len(t.Timestamp) > 0 {
			sels[Timestamp] = t.Timestamp
		}
		spec := &RSSSpec{
			URL:       t.URL,
			Target:    LinkTarget,
			Paths:     t.Paths,
			Selectors: sels,
		}
		src.Type = RSSType
		src.Spec = spec

	default:
		return nil, fmt.Errorf("unsupported source type in toml: %q", t.Type)
	}

	raw, err := json.Marshal(src.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	src.SpecRaw = raw

	h, err := hashToml(t)
	if err != nil {
		return nil, err
	}
	src.Hash = h
	return src, nil
}

// hashToml content hash of the TOML entry used by syncSources to detect
// changes. Stable JSON encoding keeps the digest deterministic across runs.
func hashToml(t *SourceTOML) ([]byte, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}
