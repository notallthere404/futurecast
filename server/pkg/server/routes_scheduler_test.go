package server

import (
	"net/http"
	"testing"
)

func TestScheduler_Run_DelegatesAndReturns200(t *testing.T) {
	t.Parallel()
	sch := &fakeScheduler{}
	s := testServer(nil, nil, nil, nil, nil, nil, sch)
	rec := doRequest(s, http.MethodPost, "/api/v1/scheduler/run", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if sch.runCalls != 1 {
		t.Errorf("Run calls = %d, want 1", sch.runCalls)
	}
}

func TestScheduler_Stop_DelegatesAndReturns200(t *testing.T) {
	t.Parallel()
	sch := &fakeScheduler{}
	s := testServer(nil, nil, nil, nil, nil, nil, sch)
	rec := doRequest(s, http.MethodPost, "/api/v1/scheduler/stop", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if sch.stopCalls != 1 {
		t.Errorf("Stop calls = %d, want 1", sch.stopCalls)
	}
}
