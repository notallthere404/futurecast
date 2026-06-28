package config

import (
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func intp(i int) *int           { return &i }
func floatp(f float64) *float64 { return &f }

// ignoreDerived Hash and SpecRaw are derived from Spec at resolve time.
// Compared separately in dedicated tests; ignore in shape compares.
// EquateEmpty normalises nil vs empty slice/map (Tags is normalised to
// []string{} in resolveCommon to satisfy a NOT NULL DB column).
var compareOpts = cmp.Options{
	cmpopts.IgnoreFields(v1.Source{}, "Hash", "SpecRaw"),
	cmpopts.EquateEmpty(),
}

var ignoreDerived = compareOpts

func TestResolve_Empty(t *testing.T) {
	t.Parallel()

	got, err := (&SourceConfig{}).Resolve()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %d entries", len(got))
	}
}

func TestResolve_RSS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   SourceConfig
		want *v1.Source
	}{
		{
			name: "inherits parent schedule via spec",
			in: SourceConfig{
				Default: SourceDefaults{Schedule: "*/10 * * * *"},
				RSS: []RSSConfig{{
					CommonFields: CommonFields{
						Name: "CISA", URL: "https://cisa.test/feed", Active: true,
					},
					Target: "description",
				}},
			},
			want: &v1.Source{
				Type:        v1.RSSType,
				Name:        "CISA",
				URL:         "https://cisa.test/feed",
				Active:      true,
				Trust:       v1.Trust(""),
				Description: "",
				Tags:        nil,
				Spec: &v1.RSSSpec{
					URL:      "https://cisa.test/feed",
					Target:   v1.SourceTarget("description"),
					Schedule: "*/10 * * * *",
				},
			},
		},
		{
			name: "child schedule overrides parent",
			in: SourceConfig{
				Default: SourceDefaults{Schedule: "*/10 * * * *"},
				RSS: []RSSConfig{{
					CommonFields: CommonFields{
						Name: "Krebs", URL: "https://krebs.test/feed", Active: true,
						Schedule: "0 * * * *",
					},
					Target: "content",
				}},
			},
			want: &v1.Source{
				Type:   v1.RSSType,
				Name:   "Krebs",
				URL:    "https://krebs.test/feed",
				Active: true,
				Spec: &v1.RSSSpec{
					URL:      "https://krebs.test/feed",
					Target:   v1.SourceTarget("content"),
					Schedule: "0 * * * *",
				},
			},
		},
		{
			name: "with paths and selectors",
			in: SourceConfig{
				RSS: []RSSConfig{{
					CommonFields: CommonFields{
						Name: "TheHackerNews", URL: "https://thn.test/", Active: true,
					},
					Target: "link",
					Paths:  []string{"*"},
					Selectors: map[string][]string{
						"nav":     {"#blog-pager > a"},
						"title":   {"h1.title"},
						"content": {"#article"},
					},
				}},
			},
			want: &v1.Source{
				Type:   v1.RSSType,
				Name:   "TheHackerNews",
				URL:    "https://thn.test/",
				Active: true,
				Spec: &v1.RSSSpec{
					URL:    "https://thn.test/",
					Target: v1.SourceTarget("link"),
					Paths:  []string{"*"},
					Selectors: v1.SelectorMap{
						v1.SelectorType("nav"):     {"#blog-pager > a"},
						v1.SelectorType("title"):   {"h1.title"},
						v1.SelectorType("content"): {"#article"},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.in.Resolve()
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("want 1 source, got %d", len(got))
			}
			if diff := cmp.Diff(tc.want, got[0], ignoreDerived); diff != "" {
				t.Errorf("source mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolve_HTTP(t *testing.T) {
	t.Parallel()

	in := SourceConfig{
		HTTP: []HTTPConfig{{
			CommonFields: CommonFields{
				Name: "VT", URL: "https://vt.test/api", Active: true, Trust: "high",
				Schedule: "*/5 * * * *",
				Auth:     &Auth{Kind: "api_key", Header: "x-apikey", Token: "secret"},
				Extract: &Extract{
					Items:     "data",
					Title:     "attr.name",
					Content:   "attr.body",
					Timestamp: "attr.ts",
					Link:      "attr.url",
				},
			},
			Method: "POST",
			Query:  map[string]string{"limit": "100"},
			Body:   `{"q":"test"}`,
			Pagination: &Pagination{
				CursorPath: "meta.cursor",
				PageParam:  "cursor",
				MaxPages:   10,
			},
		}},
	}

	want := &v1.Source{
		Type:   v1.HTTPType,
		Name:   "VT",
		URL:    "https://vt.test/api",
		Active: true,
		Trust:  v1.Trust("high"),
		Auth: &v1.Auth{
			Kind: "api_key", Header: "x-apikey", Token: "secret",
		},
		Extract: &v1.Extract{
			Items: "data", Title: "attr.name", Content: "attr.body",
			Timestamp: "attr.ts", Link: "attr.url",
		},
		Spec: &v1.HTTPSpec{
			Method:   "POST",
			Schedule: "*/5 * * * *",
			Query:    map[string]string{"limit": "100"},
			Body:     `{"q":"test"}`,
			Pagination: &v1.Pagination{
				CursorPath: "meta.cursor",
				PageParam:  "cursor",
				MaxPages:   10,
			},
		},
	}

	got, err := in.Resolve()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 source, got %d", len(got))
	}
	if diff := cmp.Diff(want, got[0], ignoreDerived); diff != "" {
		t.Errorf("source mismatch (-want +got):\n%s", diff)
	}
}

func TestResolve_Webhook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   SourceConfig
		want *v1.Source
	}{
		{
			name: "missing url synthesises from path",
			in: SourceConfig{
				Webhook: []WebhookConfig{{
					CommonFields: CommonFields{
						Name: "Alerts", Active: true,
					},
					Path:                "/alerts",
					Method:              "POST",
					ContentType:         "application/json",
					MaxBodyBytes:        1024,
					ReplayWindowSeconds: 300,
				}},
			},
			want: &v1.Source{
				Type:   v1.WebhookType,
				Name:   "Alerts",
				URL:    "webhook:///alerts",
				Active: true,
				Spec: &v1.WebhookSpec{
					Path:                "/alerts",
					Method:              "POST",
					ContentType:         "application/json",
					MaxBodyBytes:        1024,
					ReplayWindowSeconds: 300,
				},
			},
		},
		{
			name: "explicit url wins over synthesis",
			in: SourceConfig{
				Webhook: []WebhookConfig{{
					CommonFields: CommonFields{
						Name: "Alerts", URL: "https://api.test/in", Active: true,
					},
					Path:   "/alerts",
					Method: "POST",
				}},
			},
			want: &v1.Source{
				Type:   v1.WebhookType,
				Name:   "Alerts",
				URL:    "https://api.test/in",
				Active: true,
				Spec: &v1.WebhookSpec{
					Path:   "/alerts",
					Method: "POST",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.in.Resolve()
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("want 1 source, got %d", len(got))
			}
			if diff := cmp.Diff(tc.want, got[0], ignoreDerived); diff != "" {
				t.Errorf("source mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolve_Websocket(t *testing.T) {
	t.Parallel()

	in := SourceConfig{
		Websocket: []WebsocketConfig{{
			CommonFields: CommonFields{
				Name: "Feed", URL: "wss://feed.test/", Active: true,
			},
			Protocols:   []string{"v2"},
			Subscribe:   `{"action":"sub"}`,
			MessageType: "json",
			BufferSize:  256,
			Heartbeat:   &Heartbeat{IntervalMs: 30000, TimeoutMs: 10000},
			Reconnect:   &Reconnect{Enabled: true, BackoffMs: 500, MaxDelayMs: 60000},
		}},
	}

	want := &v1.Source{
		Type:   v1.WebsocketType,
		Name:   "Feed",
		URL:    "wss://feed.test/",
		Active: true,
		Spec: &v1.WebsocketSpec{
			Protocols:   []string{"v2"},
			Subscribe:   `{"action":"sub"}`,
			MessageType: "json",
			BufferSize:  256,
			Heartbeat:   &v1.Heartbeat{IntervalMs: 30000, TimeoutMs: 10000},
			Reconnect:   &v1.Reconnect{Enabled: true, BackoffMs: 500, MaxDelayMs: 60000},
		},
	}

	got, err := in.Resolve()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 source, got %d", len(got))
	}
	if diff := cmp.Diff(want, got[0], ignoreDerived); diff != "" {
		t.Errorf("source mismatch (-want +got):\n%s", diff)
	}
}

func TestResolve_TrustInheritance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		parent    string
		child     string
		wantTrust v1.Trust
	}{
		{"child empty inherits parent", "high", "", v1.Trust("high")},
		{"child set overrides parent", "high", "low", v1.Trust("low")},
		{"both empty stays empty", "", "", v1.Trust("")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := SourceConfig{
				Default: SourceDefaults{Trust: tc.parent},
				RSS: []RSSConfig{{
					CommonFields: CommonFields{
						Name: "x", URL: "https://x.test/", Trust: tc.child,
					},
				}},
			}
			got, err := in.Resolve()
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got[0].Trust != tc.wantTrust {
				t.Errorf("trust: want %q, got %q", tc.wantTrust, got[0].Trust)
			}
		})
	}
}

func TestResolve_TimeoutInheritance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		parentTimeout  int
		childTimeout   int
		wantTimeoutPtr *int
	}{
		{"child zero inherits parent", 30, 0, intp(30)},
		{"child set overrides parent", 30, 10, intp(10)},
		{"both zero remains nil", 0, 0, nil},
		{"only child set", 0, 15, intp(15)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := SourceConfig{
				Default: SourceDefaults{TimeoutSeconds: tc.parentTimeout},
				RSS: []RSSConfig{{
					CommonFields: CommonFields{
						Name: "x", URL: "https://x.test/",
						TimeoutSeconds: tc.childTimeout,
					},
				}},
			}
			got, err := in.Resolve()
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if diff := cmp.Diff(tc.wantTimeoutPtr, got[0].TimeoutSeconds); diff != "" {
				t.Errorf("timeout mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolve_HeadersInheritance(t *testing.T) {
	t.Parallel()

	parent := map[string]string{"X-Tenant": "ftc"}
	childA := map[string]string{"X-Trace": "abc"}

	tests := []struct {
		name        string
		parent      map[string]string
		child       map[string]string
		wantHeaders v1.Headers
	}{
		{"child nil inherits parent", parent, nil, v1.Headers(parent)},
		{"child set replaces parent (no merge)", parent, childA, v1.Headers(childA)},
		{"both nil stays nil", nil, nil, nil},
		{"only child set", nil, childA, v1.Headers(childA)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := SourceConfig{
				Default: SourceDefaults{Headers: tc.parent},
				RSS: []RSSConfig{{
					CommonFields: CommonFields{
						Name: "x", URL: "https://x.test/", Headers: tc.child,
					},
				}},
			}
			got, err := in.Resolve()
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if diff := cmp.Diff(tc.wantHeaders, got[0].Headers); diff != "" {
				t.Errorf("headers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolve_RetryInheritance(t *testing.T) {
	t.Parallel()

	parent := &Retry{Max: 3, BackoffMs: 500, MaxDelayMs: 5000}
	child := &Retry{Max: 1, BackoffMs: 100, MaxDelayMs: 1000}

	tests := []struct {
		name      string
		parent    *Retry
		child     *Retry
		wantRetry *v1.Retry
	}{
		{"child nil inherits parent", parent, nil, &v1.Retry{Max: 3, BackoffMs: 500, MaxDelayMs: 5000}},
		{"child set overrides parent", parent, child, &v1.Retry{Max: 1, BackoffMs: 100, MaxDelayMs: 1000}},
		{"both nil stays nil", nil, nil, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := SourceConfig{
				Default: SourceDefaults{Retry: tc.parent},
				RSS: []RSSConfig{{
					CommonFields: CommonFields{
						Name: "x", URL: "https://x.test/", Retry: tc.child,
					},
				}},
			}
			got, err := in.Resolve()
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if diff := cmp.Diff(tc.wantRetry, got[0].Retry); diff != "" {
				t.Errorf("retry mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolve_MultipleKindsOrder(t *testing.T) {
	t.Parallel()

	in := SourceConfig{
		RSS: []RSSConfig{{CommonFields: CommonFields{
			Name: "rss-a", URL: "https://a.test/",
		}}},
		HTTP: []HTTPConfig{{CommonFields: CommonFields{
			Name: "http-a", URL: "https://b.test/",
		}}},
		Webhook: []WebhookConfig{{
			CommonFields: CommonFields{Name: "wh-a"},
			Path:         "/c",
		}},
		Websocket: []WebsocketConfig{{CommonFields: CommonFields{
			Name: "ws-a", URL: "wss://d.test/",
		}}},
	}

	got, err := in.Resolve()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 sources, got %d", len(got))
	}

	wantTypes := []v1.SourceType{v1.RSSType, v1.HTTPType, v1.WebhookType, v1.WebsocketType}
	for i, want := range wantTypes {
		if got[i].Type != want {
			t.Errorf("[%d]: want type %q, got %q", i, want, got[i].Type)
		}
	}
}

func TestResolve_HashStable(t *testing.T) {
	t.Parallel()

	in := SourceConfig{
		RSS: []RSSConfig{{
			CommonFields: CommonFields{
				Name: "x", URL: "https://x.test/", Active: true, Trust: "high",
				Tags:    []string{"a", "b"},
				Headers: map[string]string{"K": "v"},
			},
			Target: "description",
		}},
	}

	a, err := in.Resolve()
	if err != nil {
		t.Fatalf("first resolve err: %v", err)
	}
	b, err := in.Resolve()
	if err != nil {
		t.Fatalf("second resolve err: %v", err)
	}

	if diff := cmp.Diff(a[0].Hash, b[0].Hash); diff != "" {
		t.Errorf("hash drift across calls (-a +b):\n%s", diff)
	}
}

func TestResolve_HashSensitive(t *testing.T) {
	t.Parallel()

	base := SourceConfig{
		RSS: []RSSConfig{{
			CommonFields: CommonFields{Name: "x", URL: "https://x.test/", Active: true},
			Target:       "description",
		}},
	}
	a, _ := base.Resolve()

	mutated := base
	mutated.RSS = []RSSConfig{{
		CommonFields: CommonFields{Name: "y", URL: "https://x.test/", Active: true},
		Target:       "description",
	}}
	b, _ := mutated.Resolve()

	if cmp.Equal(a[0].Hash, b[0].Hash) {
		t.Errorf("hash did not change after mutating Name")
	}
}

func TestLabelMap(t *testing.T) {
	t.Parallel()

	t.Run("empty classifications returns empty map", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		got := cfg.LabelMap()
		if len(got) != 0 {
			t.Errorf("want empty map, got %d entries", len(got))
		}
	})

	t.Run("single attribute three labels", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{Inference: Inference{
			Classifications: map[string][]InferenceAttribute{
				"events": {{
					Name: "vector",
					Labels: []InferenceLabel{
						{Name: "Recon"}, {Name: "Access"}, {Name: "Persistence"},
					},
				}},
			},
		}}
		got := cfg.LabelMap()

		if len(got) != 3 {
			t.Fatalf("want 3 entries, got %d", len(got))
		}
		assertIndexSet(t, got, []string{"Recon", "Access", "Persistence"})
	})

	t.Run("duplicate label across attributes deduped", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{Inference: Inference{
			Classifications: map[string][]InferenceAttribute{
				"events": {
					{
						Name:   "vector",
						Labels: []InferenceLabel{{Name: "Phishing"}, {Name: "Access"}},
					},
					{
						Name:   "sector",
						Labels: []InferenceLabel{{Name: "Phishing"}, {Name: "Finance"}},
					},
				},
			},
		}}
		got := cfg.LabelMap()

		if len(got) != 3 {
			t.Fatalf("want 3 unique labels, got %d", len(got))
		}
		assertIndexSet(t, got, []string{"Phishing", "Access", "Finance"})
	})

	t.Run("two classifications combined", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{Inference: Inference{
			Classifications: map[string][]InferenceAttribute{
				"events": {{Name: "vector", Labels: []InferenceLabel{{Name: "A"}, {Name: "B"}}}},
				"actors": {{Name: "profile", Labels: []InferenceLabel{{Name: "C"}}}},
			},
		}}
		got := cfg.LabelMap()
		if len(got) != 3 {
			t.Fatalf("want 3 entries, got %d", len(got))
		}
		assertIndexSet(t, got, []string{"A", "B", "C"})
	})
}

// assertIndexSet every expected name is a key with a unique value in [0, len).
// Specific values are NOT asserted because map iteration order varies between
// classifications; only the set of indices is verified.
func assertIndexSet(t *testing.T, got map[string]float64, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("size: want %d, got %d", len(want), len(got))
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("missing label %q in map", name)
		}
	}

	values := make([]float64, 0, len(got))
	for _, v := range got {
		values = append(values, v)
	}
	sort.Float64s(values)
	for i, v := range values {
		if v != float64(i) {
			t.Errorf("index %d: want %d, got %v", i, i, v)
		}
	}
}

func TestClassificationMap(t *testing.T) {
	t.Parallel()

	t.Run("empty returns empty map", func(t *testing.T) {
		t.Parallel()
		inf := &Inference{}
		got := inf.ClassificationMap()
		if len(got) != 0 {
			t.Errorf("want empty, got %v", got)
		}
	})

	t.Run("two attributes one classification preserves slice order", func(t *testing.T) {
		t.Parallel()
		inf := &Inference{Classifications: map[string][]InferenceAttribute{
			"events": {
				{Name: "vector"},
				{Name: "sector"},
			},
		}}
		got := inf.ClassificationMap()
		want := map[string][]string{"events": {"vector", "sector"}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("two classifications both present", func(t *testing.T) {
		t.Parallel()
		inf := &Inference{Classifications: map[string][]InferenceAttribute{
			"events": {{Name: "vector"}},
			"actors": {{Name: "profile"}},
		}}
		got := inf.ClassificationMap()
		want := map[string][]string{
			"events": {"vector"},
			"actors": {"profile"},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestEffectiveCutoff(t *testing.T) {
	t.Parallel()

	const global = 0.3

	tests := []struct {
		name string
		attr InferenceAttribute
		want float64
	}{
		{"nil pointer falls back to global", InferenceAttribute{Cutoff: nil}, global},
		{"explicit zero is honoured", InferenceAttribute{Cutoff: floatp(0.0)}, 0.0},
		{"explicit half overrides global", InferenceAttribute{Cutoff: floatp(0.5)}, 0.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.attr.EffectiveCutoff(global)
			if got != tc.want {
				t.Errorf("want %v, got %v", tc.want, got)
			}
		})
	}
}

func TestEffectiveTopN(t *testing.T) {
	t.Parallel()

	const global = 3

	tests := []struct {
		name string
		attr InferenceAttribute
		want int
	}{
		{"nil falls back to global", InferenceAttribute{TopN: nil}, global},
		{"explicit zero honoured", InferenceAttribute{TopN: intp(0)}, 0},
		{"explicit five overrides global", InferenceAttribute{TopN: intp(5)}, 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.attr.EffectiveTopN(global)
			if got != tc.want {
				t.Errorf("want %d, got %d", tc.want, got)
			}
		})
	}
}

func TestToClientConfig(t *testing.T) {
	t.Parallel()

	t.Run("empty config + empty raw", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		got := cfg.ToClientConfig("")
		if got.Raw != "" {
			t.Errorf("want empty raw, got %q", got.Raw)
		}
		if len(got.ClassMap) != 0 {
			t.Errorf("want empty class map, got %v", got.ClassMap)
		}
	})

	t.Run("classifications with labels populate ClassMap", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{Inference: Inference{
			Classifications: map[string][]InferenceAttribute{
				"events": {{
					Name: "vector",
					Labels: []InferenceLabel{
						{Name: "A"}, {Name: "B"},
					},
				}},
			},
		}}
		raw := "server:\n  port: 8765\n"
		got := cfg.ToClientConfig(raw)

		if got.Raw != raw {
			t.Errorf("raw not preserved: got %q", got.Raw)
		}

		want := map[string][]*ClientAttribute{
			"events": {{Name: "vector", Labels: []string{"A", "B"}}},
		}
		if diff := cmp.Diff(want, got.ClassMap); diff != "" {
			t.Errorf("class map mismatch (-want +got):\n%s", diff)
		}
	})
}
