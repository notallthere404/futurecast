package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func TestSources_List_OK(t *testing.T) {
	t.Parallel()
	src := &fakeSources{list: []*v1.Source{{ID: "s1", Name: "x"}}}
	s := testServer(nil, nil, src, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodGet, "/api/v1/sources", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []*v1.Source
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if len(got) != 1 || got[0].ID != "s1" {
		t.Errorf("body = %+v", got)
	}
}

func TestSources_List_Error(t *testing.T) {
	t.Parallel()
	src := &fakeSources{listErr: errors.New("db down")}
	s := testServer(nil, nil, src, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodGet, "/api/v1/sources", "")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := decodeErrorBody(t, rec.Body); got.Error.Code != "source_list_failed" {
		t.Errorf("code = %q", got.Error.Code)
	}
}

func TestSources_Upsert_Created(t *testing.T) {
	t.Parallel()
	src := &fakeSources{}
	s := testServer(nil, nil, src, nil, nil, nil, nil)

	body := `{"id":"s1","name":"feed","url":"https://x/y","type":"rss","spec":null}`
	rec := doRequest(s, http.MethodPost, "/api/v1/sources", body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if len(src.upsertCalls) != 1 || src.upsertCalls[0].ID != "s1" {
		t.Errorf("Upsert not invoked with payload: %+v", src.upsertCalls)
	}
}

func TestSources_Upsert_Error(t *testing.T) {
	t.Parallel()
	src := &fakeSources{upsertErr: errors.New("dup")}
	s := testServer(nil, nil, src, nil, nil, nil, nil)

	body := `{"id":"s1","name":"x","url":"https://x/","type":"rss","spec":null}`
	rec := doRequest(s, http.MethodPost, "/api/v1/sources", body)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSources_Upsert_BadJSON(t *testing.T) {
	t.Parallel()
	s := testServer(nil, nil, nil, nil, nil, nil, nil)
	rec := doRequest(s, http.MethodPost, "/api/v1/sources", "{")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSources_UpsertBatch_Created(t *testing.T) {
	t.Parallel()
	src := &fakeSources{}
	s := testServer(nil, nil, src, nil, nil, nil, nil)

	body := `[{"id":"a","name":"a","url":"https://a","type":"rss","spec":null},{"id":"b","name":"b","url":"https://b","type":"rss","spec":null}]`
	rec := doRequest(s, http.MethodPost, "/api/v1/sources/batch", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if len(src.batchCalls) != 1 || len(src.batchCalls[0]) != 2 {
		t.Errorf("batch not forwarded: %+v", src.batchCalls)
	}
}

func TestSources_Recent_OK(t *testing.T) {
	t.Parallel()
	src := &fakeSources{recent: []*v1.Article{{ID: "a1"}, {ID: "a2"}}}
	s := testServer(nil, nil, src, nil, nil, nil, nil)
	rec := doRequest(s, http.MethodGet, "/api/v1/articles/recent", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []*v1.Article
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if len(got) != 2 {
		t.Errorf("got %d articles", len(got))
	}
}

func TestSources_Rate_ForwardsFormat(t *testing.T) {
	t.Parallel()
	src := &fakeSources{rate: []int{1, 2, 3}}
	s := testServer(nil, nil, src, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodPost, "/api/v1/articles/rate", `{"format":"day"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(src.rateCalls) != 1 || src.rateCalls[0] != v1.Day {
		t.Errorf("rate forwarded format = %v, want day", src.rateCalls)
	}
}

func TestSources_Sync_DelegatesToRunRSS(t *testing.T) {
	t.Parallel()
	src := &fakeSources{}
	s := testServer(nil, nil, src, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodPost, "/api/v1/sources/sync", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if src.runRSSCalls != 1 {
		t.Errorf("RunRSS called %d times, want 1", src.runRSSCalls)
	}
}

func TestSources_ReaderRun_DelegatesToRunRSS(t *testing.T) {
	// The /sources/rss/run endpoint triggers an immediate fetch over
	// every active RSS source. Per-source cron entries (rss:<id>) keep
	// ticking; an accidental overlap is absorbed by article-id dedup.
	t.Parallel()
	src := &fakeSources{}
	s := testServer(nil, nil, src, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodPost, "/api/v1/sources/rss/run", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if src.runRSSCalls != 1 {
		t.Errorf("RunRSS not invoked")
	}
}

func TestSources_WebhookHandler_MountedWhenProvided(t *testing.T) {
	// When sourcesDeps returns a non-nil webhook handler, Routes()
	// mounts it at /webhooks/. When it returns nil, /webhooks/ 404s.
	t.Parallel()

	called := false
	whFn := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})

	t.Run("mounted", func(t *testing.T) {
		src := &fakeSources{webhook: whFn}
		s := testServer(nil, nil, src, nil, nil, nil, nil)
		rec := doRequest(s, http.MethodPost, "/webhooks/x", `{}`)
		if rec.Code != http.StatusAccepted {
			t.Errorf("status = %d, want 202 (handler invoked)", rec.Code)
		}
		if !called {
			t.Error("webhook handler not invoked")
		}
	})

	t.Run("not mounted", func(t *testing.T) {
		src := &fakeSources{webhook: nil}
		s := testServer(nil, nil, src, nil, nil, nil, nil)
		rec := doRequest(s, http.MethodPost, "/webhooks/x", `{}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 (no handler)", rec.Code)
		}
	})
}
