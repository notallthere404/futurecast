package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		status     int
		payload    any
		wantStatus int
		wantBody   string
	}{
		{
			name:       "ok struct",
			status:     http.StatusOK,
			payload:    map[string]string{"hello": "world"},
			wantStatus: http.StatusOK,
			wantBody:   "{\"hello\":\"world\"}\n",
		},
		{
			name:       "nil payload writes header only",
			status:     http.StatusAccepted,
			payload:    nil,
			wantStatus: http.StatusAccepted,
			wantBody:   "",
		},
		{
			name:       "error status with body",
			status:     http.StatusUnprocessableEntity,
			payload:    map[string]int{"n": 42},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   "{\"n\":42}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			writeJSON(rec, tc.status, tc.payload)
			res := rec.Result()
			defer res.Body.Close()

			if res.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", res.StatusCode, tc.wantStatus)
			}
			if got := res.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			body, _ := io.ReadAll(res.Body)
			if string(body) != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeError(discardLogger(), rec, http.StatusBadRequest, "bad_input", "missing field", errors.New("boom"))

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
	var got v1.ErrorResponse
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error.Code != "bad_input" || got.Error.Message != "missing field" {
		t.Errorf("error = %+v, want {Code: bad_input, Message: missing field}", got.Error)
	}
}

type payload struct {
	Name string `json:"name"`
}

func TestDecodeJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     string
		wantOK   bool
		wantName string
		wantCode int
	}{
		{name: "valid", body: `{"name":"alice"}`, wantOK: true, wantName: "alice", wantCode: http.StatusOK},
		{name: "invalid json", body: `not-json`, wantOK: false, wantCode: http.StatusBadRequest},
		{name: "empty body", body: ``, wantOK: false, wantCode: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			got, ok := decodeJSON[payload](rec, req, discardLogger())

			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.Name != tc.wantName {
				t.Errorf("name = %q, want %q", got.Name, tc.wantName)
			}
			if !ok {
				res := rec.Result()
				if res.StatusCode != tc.wantCode {
					t.Errorf("status = %d, want %d", res.StatusCode, tc.wantCode)
				}
				var er v1.ErrorResponse
				_ = json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&er)
				if er.Error.Code != "invalid_json" {
					t.Errorf("error code = %q, want invalid_json", er.Error.Code)
				}
			}
		})
	}
}
