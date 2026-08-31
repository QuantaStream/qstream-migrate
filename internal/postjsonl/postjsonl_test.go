package postjsonl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostDirPostsInLoadOrderAndCommits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "load-order.txt"), "region\nnation\n")
	writeFile(t, filepath.Join(dir, "region.jsonl"), `{"type":"region","data":{"r_regionkey":1}}`+"\n")
	writeFile(t, filepath.Join(dir, "nation.jsonl"), `{"type":"nation","data":{"n_nationkey":1}}`+"\n"+`{"type":"nation","data":{"n_nationkey":2}}`+"\n")

	var batches []int
	var commitCalls int
	var statsCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stats":
			statsCalls++
			_, _ = w.Write([]byte(`{"status":"ok","tables":["region","nation"]}`))
		case "/ingest/json":
			var body struct {
				Records []json.RawMessage `json:"records"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			batches = append(batches, len(body.Records))
			_, _ = fmt.Fprintf(w, `{"accepted":%d,"failed":0}`, len(body.Records))
		case "/commit":
			commitCalls++
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := PostDir(context.Background(), Options{
		InputDir:            dir,
		TargetURL:           server.URL + "/ingest/json",
		BatchSize:           2,
		Commit:              true,
		CommitAfterEachFile: true,
	})
	if err != nil {
		t.Fatalf("PostDir returned error: %v", err)
	}
	if result.Sent != 3 || result.Accepted != 3 || result.Failed != 0 || result.Batches != 2 || !result.Committed || result.CommitCalls != 2 {
		t.Fatalf("result = %+v, want three accepted rows over two batches and two commits", result)
	}
	if got, want := fmt.Sprint(batches), "[1 2]"; got != want {
		t.Fatalf("batches = %s, want %s", got, want)
	}
	if commitCalls != 2 {
		t.Fatalf("commit calls = %d, want 2", commitCalls)
	}
	if statsCalls != 1 {
		t.Fatalf("stats calls = %d, want 1", statsCalls)
	}
}

func TestPostDirCanCommitOnceAtEnd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "load-order.txt"), "region\nnation\n")
	writeFile(t, filepath.Join(dir, "region.jsonl"), `{"type":"region","data":{"r_regionkey":1}}`+"\n")
	writeFile(t, filepath.Join(dir, "nation.jsonl"), `{"type":"nation","data":{"n_nationkey":1}}`+"\n")

	var commitCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stats":
			_, _ = w.Write([]byte(`{"status":"ok","tables":["region","nation"]}`))
		case "/ingest/json":
			var body struct {
				Records []json.RawMessage `json:"records"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_, _ = fmt.Fprintf(w, `{"accepted":%d,"failed":0}`, len(body.Records))
		case "/commit":
			commitCalls++
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := PostDir(context.Background(), Options{
		InputDir:            dir,
		TargetURL:           server.URL + "/ingest/json",
		BatchSize:           1,
		Commit:              true,
		CommitAfterEachFile: false,
	})
	if err != nil {
		t.Fatalf("PostDir returned error: %v", err)
	}
	if !result.Committed || result.CommitCalls != 1 || commitCalls != 1 {
		t.Fatalf("result = %+v commitCalls = %d, want one final commit", result, commitCalls)
	}
}

func TestPostDirFailsWhenLoaderIsMissingExpectedTables(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "load-order.txt"), "part\nregion\n")
	writeFile(t, filepath.Join(dir, "part.jsonl"), `{"type":"part","data":{"p_partkey":1}}`+"\n")
	writeFile(t, filepath.Join(dir, "region.jsonl"), `{"type":"region","data":{"r_regionkey":1}}`+"\n")

	var postCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stats":
			_, _ = w.Write([]byte(`{"status":"ok","tables":["region"]}`))
		case "/ingest/json":
			postCalls++
			_, _ = w.Write([]byte(`{"accepted":1,"failed":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := PostDir(context.Background(), Options{
		InputDir:  dir,
		TargetURL: server.URL + "/ingest/json",
		BatchSize: 1,
		Commit:    false,
	})
	if err == nil || !strings.Contains(err.Error(), "missing expected table(s): part") {
		t.Fatalf("expected missing table guard error, got %v", err)
	}
	if postCalls != 0 {
		t.Fatalf("post calls = %d, want 0", postCalls)
	}
}

func TestPostDirUsesSortedJSONLWhenNoLoadOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "b.jsonl"), `{"type":"b","data":{"id":1}}`+"\n")
	writeFile(t, filepath.Join(dir, "a.jsonl"), `{"type":"a","data":{"id":1}}`+"\n")

	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seen = append(seen, body.Records[0]["type"].(string))
		_, _ = fmt.Fprintf(w, `{"accepted":%d,"failed":0}`, len(body.Records))
	}))
	defer server.Close()

	result, err := PostDir(context.Background(), Options{InputDir: dir, TargetURL: server.URL, BatchSize: 1})
	if err != nil {
		t.Fatalf("PostDir returned error: %v", err)
	}
	if result.Sent != 2 {
		t.Fatalf("sent = %d, want 2", result.Sent)
	}
	if got, want := strings.Join(seen, ","), "a,b"; got != want {
		t.Fatalf("seen = %s, want %s", got, want)
	}
}

func TestPostDirFailsOnLoaderRejectedBatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "events.jsonl"), `{"type":"events","data":{"id":1}}`+"\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"accepted":0,"failed":1,"errors":["no route"]}`))
	}))
	defer server.Close()

	_, err := PostDir(context.Background(), Options{InputDir: dir, TargetURL: server.URL, BatchSize: 1})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("expected HTTP rejection error, got %v", err)
	}
}

func TestPostDirFailsOnInvalidJSONL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "events.jsonl"), "{not json}\n")

	_, err := PostDir(context.Background(), Options{InputDir: dir, TargetURL: "http://example.invalid"})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected invalid JSON error, got %v", err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
