package measure

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wool/internal/llamaserver"
)

func fakeCompletion(t *testing.T, body string, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/completion" {
			http.NotFound(w, r)
			return
		}
		if delay > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
}

func TestRunSmokeSuccess(t *testing.T) {
	srv := fakeCompletion(t, `{"content":"ok","stop":true,"tokens_predicted":1}`, 0)
	defer srv.Close()
	res := RunSmoke(context.Background(), llamaserver.NewClientURL(srv.URL), time.Second)
	if !res.OK() {
		t.Fatalf("smoke failed: %q", res.Err)
	}
	if strings.TrimSpace(res.Content) != "ok" {
		t.Fatalf("content = %q", res.Content)
	}
	if res.Latency <= 0 || res.At.IsZero() {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunSmokeEmptyOutput(t *testing.T) {
	srv := fakeCompletion(t, `{"content":"   ","stop":true}`, 0)
	defer srv.Close()
	res := RunSmoke(context.Background(), llamaserver.NewClientURL(srv.URL), time.Second)
	if res.OK() || res.Err != "empty response" {
		t.Fatalf("result = %+v, want empty response", res)
	}
}

func TestRunSmokeTimeout(t *testing.T) {
	srv := fakeCompletion(t, `{"content":"ok"}`, time.Second)
	defer srv.Close()
	start := time.Now()
	res := RunSmoke(context.Background(), llamaserver.NewClientURL(srv.URL), 20*time.Millisecond)
	if res.OK() || res.Err != "timeout" {
		t.Fatalf("result = %+v, want timeout", res)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestRunSmokeServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	res := RunSmoke(context.Background(), llamaserver.NewClientURL(srv.URL), time.Second)
	if res.OK() || !strings.Contains(res.Err, "boom") {
		t.Fatalf("result = %+v, want a short HTTP error", res)
	}
}

func TestLatestVerification(t *testing.T) {
	s := NewStoreAt(filepath.Join(t.TempDir(), "runs.jsonl"))
	base := Record{ModelID: "m", Ctx: 4096}
	ok := base
	ok.At = time.Unix(1_700_000_000, 0)
	ok.VerifiedAt = ok.At
	ok.SmokeMS = 420
	if err := s.Append(ok); err != nil {
		t.Fatal(err)
	}
	fail := base
	fail.At = time.Unix(1_700_000_100, 0)
	fail.SmokeError = "empty response"
	fail.SmokeMS = 90
	if err := s.Append(fail); err != nil {
		t.Fatal(err)
	}
	// A newer record without a smoke attempt must not hide the last result.
	plain := base
	plain.At = time.Unix(1_700_000_200, 0)
	if err := s.Append(plain); err != nil {
		t.Fatal(err)
	}

	got, found := s.LatestVerification(base.Shape())
	if !found {
		t.Fatal("no verification")
	}
	if got.OK() || got.Err != "empty response" || got.LatencyMS != 90 {
		t.Fatalf("verification = %+v, want the newest failure", got)
	}
	if got.At.Unix() != 1_700_000_100 {
		t.Fatalf("failure at = %s, want record time", got.At)
	}
	if _, found := s.LatestVerification(Shape{ModelID: "other"}); found {
		t.Fatal("unexpected verification for another shape")
	}
}
