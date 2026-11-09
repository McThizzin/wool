package bench

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"wool/internal/llamaserver"
	"wool/internal/measure"
)

func testRunner(t *testing.T, h http.HandlerFunc, opts Options) *Runner {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Runner{
		Client:  &llamaserver.Client{BaseURL: srv.URL, HTTP: srv.Client()},
		Options: opts,
	}
}

func TestRunnerUsesTimings(t *testing.T) {
	var sawRequest llamaserver.CompletionRequest
	completions := 0
	r := testRunner(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/completion":
			if err := json.NewDecoder(req.Body).Decode(&sawRequest); err != nil {
				t.Errorf("decode request: %v", err)
			}
			completions++
			io.WriteString(w, `{
				"content":"ok",
				"tokens_evaluated":20,
				"tokens_predicted":8,
				"timings":{
					"prompt_n":20,"prompt_ms":100,"prompt_per_second":200,
					"predicted_n":8,"predicted_ms":50,"predicted_per_second":160
				}
			}`)
		case "/metrics":
			io.WriteString(w, "")
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
		}
	}, Options{Runs: 2, PromptTokens: 20, GenerationTokens: 8, Seed: 7})

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if completions != 2 {
		t.Fatalf("completions = %d, want 2", completions)
	}
	if sawRequest.Temperature != 0 || sawRequest.Seed != 7 || sawRequest.CachePrompt || sawRequest.Stream {
		t.Fatalf("request = %+v", sawRequest)
	}
	if len(res.Runs) != 2 {
		t.Fatalf("runs = %d", len(res.Runs))
	}
	for _, run := range res.Runs {
		if run.Source != "timings" || run.PromptTPS != 200 || run.GenerationTPS != 160 {
			t.Fatalf("run = %+v", run)
		}
		if run.PromptTokens != 20 || run.GenerationTokens != 8 || run.TotalMS != 150 {
			t.Fatalf("run tokens = %+v", run)
		}
	}
	if res.Median.PromptTPS != 200 || res.Median.GenerationTPS != 160 || res.Median.TotalMS != 150 {
		t.Fatalf("median = %+v", res.Median)
	}
	if res.Prompt == "" {
		t.Fatal("prompt is empty")
	}
}

func TestRunnerFallsBackToMetrics(t *testing.T) {
	metricsCalls := 0
	r := testRunner(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/metrics":
			metricsCalls++
			// Each pair of samples adds 100 prompt tokens over 2s and 200
			// generated tokens over 4s, so both rates are 50 tok/s.
			n := metricsCalls / 2
			io.WriteString(w, strings.Join([]string{
				"llamacpp:prompt_tokens_total " + strconv.Itoa(100+100*n),
				"llamacpp:prompt_seconds_total " + strconv.Itoa(1+2*n),
				"llamacpp:tokens_predicted_total " + strconv.Itoa(200+200*n),
				"llamacpp:tokens_predicted_seconds_total " + strconv.Itoa(2+4*n),
			}, "\n"))
		case "/completion":
			io.WriteString(w, `{"content":"ok","tokens_evaluated":1,"tokens_predicted":1}`)
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
		}
	}, Options{Runs: 2, PromptTokens: 10, GenerationTokens: 4})

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if metricsCalls != 4 {
		t.Fatalf("metrics calls = %d, want 4", metricsCalls)
	}
	for _, run := range res.Runs {
		if run.Source != "metrics" {
			t.Fatalf("source = %q", run.Source)
		}
		if run.PromptTPS != 50 || run.GenerationTPS != 50 {
			t.Fatalf("run = %+v", run)
		}
		if run.PromptTokens != 100 || run.GenerationTokens != 200 {
			t.Fatalf("tokens = %+v", run)
		}
	}
}

func TestRunnerFallsBackToWallTime(t *testing.T) {
	base := time.Unix(1000, 0)
	times := []time.Time{
		base, base.Add(2 * time.Second),
		base.Add(3 * time.Second), base.Add(6 * time.Second),
	}
	i := 0
	r := testRunner(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/metrics":
			http.NotFound(w, req)
		case "/completion":
			io.WriteString(w, `{"content":"ok","tokens_evaluated":10,"tokens_predicted":5}`)
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
		}
	}, Options{Runs: 2, PromptTokens: 10, GenerationTokens: 5})
	r.Now = func() time.Time {
		if i >= len(times) {
			return times[len(times)-1]
		}
		v := times[i]
		i++
		return v
	}

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Runs) != 2 {
		t.Fatalf("runs = %d", len(res.Runs))
	}
	if res.Runs[0].Source != "wall" || res.Runs[0].TotalMS != 2000 {
		t.Fatalf("first run = %+v", res.Runs[0])
	}
	if res.Runs[0].PromptTPS != 5 || res.Runs[0].GenerationTPS != 2.5 {
		t.Fatalf("first rates = %+v", res.Runs[0])
	}
	if res.Runs[1].TotalMS != 3000 {
		t.Fatalf("second run = %+v", res.Runs[1])
	}
}

func TestRunnerWarmup(t *testing.T) {
	var predicts []int
	r := testRunner(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/metrics":
			io.WriteString(w, "")
		case "/completion":
			var body llamaserver.CompletionRequest
			json.NewDecoder(req.Body).Decode(&body)
			predicts = append(predicts, body.NPredict)
			io.WriteString(w, `{"content":"ok","timings":{"prompt_n":1,"predicted_n":1}}`)
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
		}
	}, Options{Runs: 1, PromptTokens: 10, GenerationTokens: 8, Warmup: true})

	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(predicts) != 2 {
		t.Fatalf("requests = %v, want warmup plus one run", predicts)
	}
	if predicts[0] != 8 || predicts[1] != 8 {
		t.Fatalf("n_predict = %v", predicts)
	}
}

func TestSummarize(t *testing.T) {
	runs := []measure.BenchRun{
		{PromptTPS: 30, GenerationTPS: 3, TotalMS: 300},
		{PromptTPS: 10, GenerationTPS: 1, TotalMS: 100},
		{PromptTPS: 20, GenerationTPS: 2, TotalMS: 200},
	}
	median, min, max := Summarize(runs)
	if median.PromptTPS != 20 || median.GenerationTPS != 2 || median.TotalMS != 200 {
		t.Fatalf("median = %+v", median)
	}
	if min.PromptTPS != 10 || max.PromptTPS != 30 {
		t.Fatalf("min/max = %+v / %+v", min, max)
	}

	even, _, _ := Summarize([]measure.BenchRun{
		{PromptTPS: 10, GenerationTPS: 1, TotalMS: 100},
		{PromptTPS: 20, GenerationTPS: 2, TotalMS: 200},
	})
	if even.PromptTPS != 15 || even.GenerationTPS != 1.5 {
		t.Fatalf("even median = %+v", even)
	}
}

func TestBuildPrompt(t *testing.T) {
	if got := BuildPrompt(0); got != "Hello" {
		t.Fatalf("BuildPrompt(0) = %q", got)
	}
	got := BuildPrompt(50)
	if !strings.Contains(got, promptSentence) || strings.Count(got, promptSentence) != 5 {
		t.Fatalf("BuildPrompt(50) = %q", got)
	}
}
