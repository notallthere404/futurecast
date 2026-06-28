package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/notallthere404/futurecast/server/pkg/config"
)

func TestSystem_Status_OK(t *testing.T) {
	t.Parallel()
	s := testServer(nil, nil, nil, nil, nil, nil, nil)
	rec := doRequest(s, http.MethodGet, "/api/v1/system/status", "")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestSystem_Config_OK(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{clientCfg: config.ClientConfig{Raw: "yaml content"}}
	s := testServer(cfg, nil, nil, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodGet, "/api/v1/system/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got config.ClientConfig
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Raw != "yaml content" {
		t.Errorf("Raw = %q", got.Raw)
	}
}

func TestSystem_Config_Error(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{err: errors.New("read")}
	s := testServer(cfg, nil, nil, nil, nil, nil, nil)
	rec := doRequest(s, http.MethodGet, "/api/v1/system/config", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSystem_ConfigUpdate_OK(t *testing.T) {
	// On success the handler returns {"error":""} rather than the
	// structured error envelope; the dashboard's config editor
	// expects this shape so it can render validation messages inline.
	t.Parallel()
	sys := &fakeSystem{}
	s := testServer(nil, sys, nil, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodPut, "/api/v1/system/config", `{"config":"new yaml"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got["error"] != "" {
		t.Errorf(`body["error"] = %q, want ""`, got["error"])
	}
	if len(sys.updateCalls) != 1 || sys.updateCalls[0] != "new yaml" {
		t.Errorf("UpdateConfig payload = %+v", sys.updateCalls)
	}
}

func TestSystem_ConfigUpdate_ValidationError(t *testing.T) {
	t.Parallel()
	sys := &fakeSystem{updateErr: errors.New("invalid schedule")}
	s := testServer(nil, sys, nil, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodPut, "/api/v1/system/config", `{"config":"bad"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var got map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got["error"] != "invalid schedule" {
		t.Errorf(`body["error"] = %q, want "invalid schedule"`, got["error"])
	}
}

func TestSystem_ConfigUpdate_BadJSON(t *testing.T) {
	t.Parallel()
	s := testServer(nil, nil, nil, nil, nil, nil, nil)
	rec := doRequest(s, http.MethodPut, "/api/v1/system/config", "not-json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSystem_Restart_OK(t *testing.T) {
	t.Parallel()
	sys := &fakeSystem{}
	s := testServer(nil, sys, nil, nil, nil, nil, nil)
	rec := doRequest(s, http.MethodPost, "/api/v1/system/restart", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if sys.restartCalls != 1 {
		t.Errorf("Restart calls = %d, want 1", sys.restartCalls)
	}
}

func TestSystem_Restart_Error(t *testing.T) {
	t.Parallel()
	sys := &fakeSystem{restartErr: errors.New("boom")}
	s := testServer(nil, sys, nil, nil, nil, nil, nil)
	rec := doRequest(s, http.MethodPost, "/api/v1/system/restart", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := decodeErrorBody(t, rec.Body); got.Error.Code != "restart_failed" {
		t.Errorf("code = %q", got.Error.Code)
	}
}

func TestSystem_UptimeTotal_OK(t *testing.T) {
	t.Parallel()
	sys := &fakeSystem{uptimeTotal: 0.95}
	s := testServer(nil, sys, nil, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodPost, "/api/v1/system/uptime", `{"start":"S","end":"E"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(sys.totalCalls) != 1 || sys.totalCalls[0].start != "S" || sys.totalCalls[0].end != "E" {
		t.Errorf("UptimeTotal args = %+v", sys.totalCalls)
	}
}

func TestSystem_UptimeSegment_ForwardsFormat(t *testing.T) {
	t.Parallel()
	sys := &fakeSystem{uptimeSeg: []float64{99.5, 99.8}}
	s := testServer(nil, sys, nil, nil, nil, nil, nil)

	rec := doRequest(s, http.MethodPost, "/api/v1/system/uptime/rate", `{"format":"month"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(sys.segCalls) != 1 || string(sys.segCalls[0]) != "month" {
		t.Errorf("UptimeSegment forwarded = %v", sys.segCalls)
	}
}
