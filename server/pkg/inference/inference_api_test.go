package inference

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestParseLabelScores_FlatObject(t *testing.T) {
	t.Parallel()
	got, err := parseLabelScores(`{"Reconnaissance": 0.82, "Persistence": 0.4}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d scores, want 2", len(got))
	}
}

func TestParseLabelScores_StripsCodeFences(t *testing.T) {
	t.Parallel()
	got, err := parseLabelScores("```json\n{\"A\": 0.5}\n```")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 || got[0].Label != "A" || got[0].Score != 0.5 {
		t.Errorf("got %+v", got)
	}
}

func TestParseLabelScores_UnwrapsSingleKey(t *testing.T) {
	t.Parallel()
	// Model wraps under a top-level key (common with "labels" or "scores").
	got, err := parseLabelScores(`{"labels": {"A": 0.9, "B": 0.3}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
}

func TestParseLabelScores_AcceptsStringNumbers(t *testing.T) {
	t.Parallel()
	got, err := parseLabelScores(`{"A": "0.75"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 || got[0].Score != 0.75 {
		t.Errorf("got %+v", got)
	}
}

func TestParseLabelScores_InvalidJSON(t *testing.T) {
	t.Parallel()
	if _, err := parseLabelScores(`not json`); err == nil {
		t.Error("expected error")
	}
}

func TestFilterAndTrim_CutoffAndTopN(t *testing.T) {
	t.Parallel()
	scores := []*v1.LabelScore{
		{Label: "A", Score: 0.9},
		{Label: "B", Score: 0.7},
		{Label: "C", Score: 0.5},
		{Label: "D", Score: 0.1},
	}
	got := filterAndTrim(scores, 0.6, 2)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].Label != "A" || got[1].Label != "B" {
		t.Errorf("sort/trim wrong: %+v", got)
	}
}

func TestFilterAndTrim_AllBelowCutoff_ReturnsSentinel(t *testing.T) {
	t.Parallel()
	got := filterAndTrim([]*v1.LabelScore{{Label: "X", Score: 0.1}}, 0.75, 3)
	if len(got) != 1 || got[0].Label != "n/i" || got[0].Score != 0.0 {
		t.Errorf("sentinel missing: %+v", got)
	}
}

func TestBuildClassifyPrompt_IncludesInstructionAndDefinitions(t *testing.T) {
	t.Parallel()
	got := buildClassifyPrompt(v1.AttributeSpec{
		Instruction: "Classify cyber threat tactics.",
		Labels: []v1.LabelSpec{
			{Name: "Recon", Definition: "intel gathering"},
			{Name: "Impact"},
		},
		Cutoff: 0.5,
	})
	for _, want := range []string{"Classify cyber threat tactics.", "Recon: intel gathering", "- Impact", "at or above 0.50", "Return JSON only"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q\n--- prompt ---\n%s", want, got)
		}
	}
}

func TestClassifyAPI_HappyPath(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("auth header = %q", auth)
		}
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Model != "test-model" {
			t.Errorf("model = %q", body.Model)
		}

		resp := chatResponse{
			Choices: []struct {
				Message chatMessage `json:"message"`
			}{
				{Message: chatMessage{Role: "assistant", Content: `{"Reconnaissance": 0.92, "Execution": 0.3}`}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	c := New(discardLogger())
	c.SetTarget(Target{
		Type:     "api",
		Endpoint: server.URL,
		APIKey:   "test-key",
		Model:    "test-model",
	})

	spec := []v1.ClassificationSpec{{
		Name: "events",
		Attributes: []v1.AttributeSpec{{
			Name:   "tactic",
			Labels: []v1.LabelSpec{{Name: "Reconnaissance"}, {Name: "Execution"}},
			Cutoff: 0.5,
			TopN:   3,
		}},
	}}

	art := &v1.ClassifyArticle{ID: "00000000-0000-0000-0000-000000000001", Content: "scanning hosts", Timestamp: time.Now()}
	out, err := c.classifyAPI(context.Background(), art, spec, c.Target())
	if err != nil {
		t.Fatalf("classifyAPI: %v", err)
	}
	if len(out) != 1 || out[0].Classification != "events" {
		t.Fatalf("response shape: %+v", out)
	}
	scores := out[0].Data["tactic"]
	if len(scores) != 1 || scores[0].Label != "Reconnaissance" {
		t.Errorf("cutoff not applied; scores = %+v", scores)
	}
}

func TestClassifyAPI_MissingCreds_Errors(t *testing.T) {
	t.Parallel()
	c := New(discardLogger())
	c.SetTarget(Target{Type: "api", Endpoint: "", APIKey: "", Model: "x"})

	_, err := c.classifyAPI(context.Background(),
		&v1.ClassifyArticle{ID: "x", Content: "hi", Timestamp: time.Now()},
		[]v1.ClassificationSpec{{Name: "e", Attributes: []v1.AttributeSpec{{Name: "a", Labels: []v1.LabelSpec{{Name: "L"}}}}}},
		c.Target(),
	)
	if err == nil {
		t.Error("expected error for missing endpoint + api_key")
	}
}

func TestClassifyAPI_RemoteErrorBubbles(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	t.Cleanup(server.Close)

	c := New(discardLogger())
	c.SetTarget(Target{Type: "api", Endpoint: server.URL, APIKey: "k", Model: "m"})

	_, err := c.classifyAPI(context.Background(),
		&v1.ClassifyArticle{ID: "x", Content: "hi", Timestamp: time.Now()},
		[]v1.ClassificationSpec{{Name: "e", Attributes: []v1.AttributeSpec{{Name: "a", Labels: []v1.LabelSpec{{Name: "L"}}}}}},
		c.Target(),
	)
	if err == nil || !strings.Contains(err.Error(), "bad status 401") {
		t.Errorf("got err=%v, want 401-status error", err)
	}
}
