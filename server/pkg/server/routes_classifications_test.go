package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

// doRequest fires a request through the assembled Server's mux and
// returns the recorded response. Exercising the full Routes() means
// tests cover both the routing table and the handler body.
func doRequest(s *Server, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, r)
	return rec
}

// decodeErrorBody pulls the structured error envelope writeError emits.
func decodeErrorBody(t *testing.T, body io.Reader) v1.ErrorResponse {
	t.Helper()
	var got v1.ErrorResponse
	if err := json.NewDecoder(body).Decode(&got); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return got
}

func TestClassifications_Search_OK(t *testing.T) {
	t.Parallel()
	cls := &fakeClassifications{search: []*v1.LinkedClassification{{Title: "abc"}}}
	s := testServer(nil, nil, nil, nil, cls, nil, nil)

	rec := doRequest(s, http.MethodGet, "/api/v1/classifications?classification=events&title=cve&limit=10&cutoff=0.4&label=foo&label=bar", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cls.lastQuery.Classification != "events" || cls.lastQuery.Title != "cve" {
		t.Errorf("query not forwarded: %+v", cls.lastQuery)
	}
	if cls.lastQuery.Limit != 10 {
		t.Errorf("limit = %d, want 10", cls.lastQuery.Limit)
	}
	if cls.lastQuery.Cutoff != 0.4 {
		t.Errorf("cutoff = %f, want 0.4", cls.lastQuery.Cutoff)
	}
	if len(cls.lastQuery.Labels) != 2 || cls.lastQuery.Labels[0] != "foo" {
		t.Errorf("labels = %v", cls.lastQuery.Labels)
	}
}

func TestClassifications_Search_Error(t *testing.T) {
	t.Parallel()
	cls := &fakeClassifications{searchErr: errors.New("boom")}
	s := testServer(nil, nil, nil, nil, cls, nil, nil)

	rec := doRequest(s, http.MethodGet, "/api/v1/classifications?classification=x", "")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := decodeErrorBody(t, rec.Body); got.Error.Code != "query_failed" {
		t.Errorf("code = %q, want query_failed", got.Error.Code)
	}
}

func TestClassifications_Count_OK(t *testing.T) {
	t.Parallel()
	cls := &fakeClassifications{count: 42}
	s := testServer(nil, nil, nil, nil, cls, nil, nil)

	rec := doRequest(s, http.MethodPost, "/api/v1/classifications/count",
		`{"classification":"events","start":"2025-01-01","end":"2025-02-01"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var n int
	_ = json.NewDecoder(rec.Body).Decode(&n)
	if n != 42 {
		t.Errorf("body = %d, want 42", n)
	}
}

func TestClassifications_Count_BadJSON(t *testing.T) {
	t.Parallel()
	s := testServer(nil, nil, nil, nil, nil, nil, nil)
	rec := doRequest(s, http.MethodPost, "/api/v1/classifications/count", "not-json")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := decodeErrorBody(t, rec.Body); got.Error.Code != "invalid_json" {
		t.Errorf("code = %q, want invalid_json", got.Error.Code)
	}
}

func TestClassifications_Run_KicksInferenceWorker(t *testing.T) {
	// The /classifications/run endpoint is a manual trigger for the
	// inference worker. It returns 200 immediately and kicks the
	// worker; classification happens in the background.
	t.Parallel()
	inf := &fakeInference{}
	s := testServer(nil, nil, nil, nil, nil, inf, nil)

	rec := doRequest(s, http.MethodPost, "/api/v1/classifications/run", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if inf.kicks != 1 {
		t.Errorf("inference.Kick called %d times, want 1", inf.kicks)
	}
}

func TestClassifications_Metrics_ForwardsQueryParams(t *testing.T) {
	t.Parallel()
	cls := &fakeClassifications{metrics: map[string]*v1.Signal{"a": {}}}
	s := testServer(nil, nil, nil, nil, cls, nil, nil)

	rec := doRequest(s, http.MethodGet,
		"/api/v1/classifications/metrics?classification=events&label=a&label=b&start=S&end=E", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if cls.lastMetrics.classification != "events" || cls.lastMetrics.start != "S" || cls.lastMetrics.end != "E" {
		t.Errorf("metrics args lost: %+v", cls.lastMetrics)
	}
	if len(cls.lastMetrics.labels) != 2 {
		t.Errorf("labels = %v, want 2", cls.lastMetrics.labels)
	}
}

func TestClassifications_Heatmap_Treemap_Plot_Scatter_Quadrant(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		path   string
		body   string
		setOK  func(*fakeClassifications)
		setErr func(*fakeClassifications)
	}{
		{
			name:   "heatmap",
			path:   "/api/v1/visualizations/heatmap",
			body:   `{"classification":"events","label":"x","start":"S","end":"E"}`,
			setOK:  func(f *fakeClassifications) { f.heatmap = []*v1.LabelWeight{{}} },
			setErr: func(f *fakeClassifications) { f.heatmapErr = errors.New("x") },
		},
		{
			name:   "treemap",
			path:   "/api/v1/visualizations/treemap",
			body:   `{"classification":"events","attribute":"vector","start":"S","end":"E","cutoff":0.1}`,
			setOK:  func(f *fakeClassifications) { f.treemap = []*v1.LabelCount{{}} },
			setErr: func(f *fakeClassifications) { f.treemapErr = errors.New("x") },
		},
		{
			name:   "plot",
			path:   "/api/v1/visualizations/plot",
			body:   `{"classification":"events","label":"x","start":"S","end":"E"}`,
			setOK:  func(f *fakeClassifications) { f.plot = []*v1.PlotPoint{{}} },
			setErr: func(f *fakeClassifications) { f.plotErr = errors.New("x") },
		},
		{
			name:   "scatter",
			path:   "/api/v1/visualizations/scatter",
			body:   `{"classification":"events","attribute":"vector","cutoff":0,"labels":[],"start":"","end":""}`,
			setOK:  func(f *fakeClassifications) { f.scatter = []*v1.ScatterPoint{{}} },
			setErr: func(f *fakeClassifications) { f.scatterErr = errors.New("x") },
		},
		{
			name:   "quadrant",
			path:   "/api/v1/visualizations/quadrant",
			body:   `{"classification":"events","label":"x","a":{"start":"","end":""},"b":{"start":"","end":""}}`,
			setOK:  func(f *fakeClassifications) { f.quadrant = []*v1.LabelFrequencyAverage{{Frequency: 1}} },
			setErr: func(f *fakeClassifications) { f.quadrantErr = errors.New("x") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/ok", func(t *testing.T) {
			t.Parallel()
			cls := &fakeClassifications{}
			tc.setOK(cls)
			s := testServer(nil, nil, nil, nil, cls, nil, nil)
			rec := doRequest(s, http.MethodPost, tc.path, tc.body)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
		})
		t.Run(tc.name+"/error", func(t *testing.T) {
			t.Parallel()
			cls := &fakeClassifications{}
			tc.setErr(cls)
			s := testServer(nil, nil, nil, nil, cls, nil, nil)
			rec := doRequest(s, http.MethodPost, tc.path, tc.body)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rec.Code)
			}
		})
		t.Run(tc.name+"/bad_json", func(t *testing.T) {
			t.Parallel()
			s := testServer(nil, nil, nil, nil, nil, nil, nil)
			rec := doRequest(s, http.MethodPost, tc.path, "not-json")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestClassifications_InsertBatch_ForwardsPayload(t *testing.T) {
	t.Parallel()
	cls := &fakeClassifications{}
	s := testServer(nil, nil, nil, nil, cls, nil, nil)

	body := `[{"classification":"events","id":"i1","article_id":"a1","timestamp":"2025-01-01T00:00:00Z","data":{}}]`
	rec := doRequest(s, http.MethodPost, "/api/v1/classifications/upload", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(cls.insertCalls) != 1 || len(cls.insertCalls[0]) != 1 {
		t.Errorf("InsertBatch payload not forwarded: %+v", cls.insertCalls)
	}
	if cls.insertCalls[0][0].Classification != "events" {
		t.Errorf("classification lost: %+v", cls.insertCalls[0][0])
	}
}
