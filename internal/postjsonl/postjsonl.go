package postjsonl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Options struct {
	InputDir            string
	TargetURL           string
	BatchSize           int
	CommitURL           string
	LoaderStatsURL      string
	SkipLoaderCheck     bool
	Commit              bool
	CommitAfterEachFile bool
	HTTPClient          *http.Client
}

type TableResult struct {
	Table    string
	Path     string
	Sent     int64
	Accepted int64
	Failed   int64
	Batches  int64
}

type Result struct {
	Tables      []TableResult
	Sent        int64
	Accepted    int64
	Failed      int64
	Batches     int64
	Committed   bool
	CommitCalls int64
}

type ingestResponse struct {
	Accepted int      `json:"accepted"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors"`
}

type loaderStats struct {
	Status string   `json:"status"`
	Tables []string `json:"tables"`
}

func PostDir(ctx context.Context, opts Options) (Result, error) {
	opts = normalizeOptions(opts)
	order, err := tableOrder(opts.InputDir)
	if err != nil {
		return Result{}, err
	}
	if err := verifyLoaderTables(ctx, opts, order); err != nil {
		return Result{}, err
	}

	var result Result
	for _, table := range order {
		tableResult, err := postTable(ctx, table, filepath.Join(opts.InputDir, table+".jsonl"), opts)
		if err != nil {
			return Result{}, err
		}
		result.Tables = append(result.Tables, tableResult)
		result.Sent += tableResult.Sent
		result.Accepted += tableResult.Accepted
		result.Failed += tableResult.Failed
		result.Batches += tableResult.Batches
		if opts.Commit && opts.CommitAfterEachFile {
			if err := postCommit(ctx, opts); err != nil {
				return Result{}, fmt.Errorf("commit after table %s: %w", table, err)
			}
			result.Committed = true
			result.CommitCalls++
		}
	}
	if opts.Commit && !opts.CommitAfterEachFile {
		if err := postCommit(ctx, opts); err != nil {
			return Result{}, err
		}
		result.Committed = true
		result.CommitCalls++
	}
	return result, nil
}

func normalizeOptions(opts Options) Options {
	if opts.InputDir == "" {
		opts.InputDir = "exports"
	}
	if opts.TargetURL == "" {
		opts.TargetURL = "http://127.0.0.1:8088/ingest/json"
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 2000
	}
	if opts.CommitURL == "" && strings.HasSuffix(opts.TargetURL, "/ingest/json") {
		opts.CommitURL = strings.TrimSuffix(opts.TargetURL, "/ingest/json") + "/commit"
	}
	if opts.LoaderStatsURL == "" {
		opts.LoaderStatsURL = deriveStatsURL(opts.TargetURL)
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return opts
}

func deriveStatsURL(targetURL string) string {
	targetURL = strings.TrimRight(targetURL, "/")
	if !strings.HasSuffix(targetURL, "/ingest/json") {
		return ""
	}
	return strings.TrimSuffix(targetURL, "/ingest/json") + "/stats"
}

func tableOrder(inputDir string) ([]string, error) {
	orderPath := filepath.Join(inputDir, "load-order.txt")
	if payload, err := os.ReadFile(orderPath); err == nil {
		var order []string
		for _, line := range strings.Split(string(payload), "\n") {
			table := strings.TrimSpace(line)
			if table != "" {
				order = append(order, table)
			}
		}
		if len(order) > 0 {
			return order, nil
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", orderPath, err)
	}

	paths, err := filepath.Glob(filepath.Join(inputDir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no JSONL files found in %s", inputDir)
	}
	order := make([]string, 0, len(paths))
	for _, path := range paths {
		order = append(order, strings.TrimSuffix(filepath.Base(path), ".jsonl"))
	}
	sort.Strings(order)
	return order, nil
}

func verifyLoaderTables(ctx context.Context, opts Options, expected []string) error {
	if opts.SkipLoaderCheck || opts.LoaderStatsURL == "" {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.LoaderStatsURL, nil)
	if err != nil {
		return fmt.Errorf("loader table guard: %w", err)
	}
	response, err := opts.HTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("loader table guard: read %s: %w", opts.LoaderStatsURL, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("loader table guard: read response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("loader table guard: HTTP %d from %s: %s", response.StatusCode, opts.LoaderStatsURL, strings.TrimSpace(string(body)))
	}
	var stats loaderStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return fmt.Errorf("loader table guard: decode %s: %w", opts.LoaderStatsURL, err)
	}
	if stats.Status != "" && stats.Status != "ok" {
		return fmt.Errorf("loader table guard: loader status is %q", stats.Status)
	}
	if len(stats.Tables) == 0 {
		return fmt.Errorf("loader table guard: %s did not report mounted table names", opts.LoaderStatsURL)
	}
	mounted := make(map[string]bool, len(stats.Tables))
	for _, table := range stats.Tables {
		mounted[table] = true
	}
	var missing []string
	for _, table := range expected {
		if !mounted[table] {
			missing = append(missing, table)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("loader table guard: target %s is missing expected table(s): %s; mounted table(s): %s", opts.LoaderStatsURL, strings.Join(missing, ", "), strings.Join(stats.Tables, ", "))
	}
	return nil
}

func postTable(ctx context.Context, table, path string, opts Options) (TableResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return TableResult{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	result := TableResult{Table: table, Path: path}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	batch := make([]json.RawMessage, 0, opts.BatchSize)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			return TableResult{}, fmt.Errorf("%s contains invalid JSON", path)
		}
		batch = append(batch, append(json.RawMessage(nil), line...))
		if len(batch) >= opts.BatchSize {
			reply, err := postBatch(ctx, opts, batch)
			if err != nil {
				return TableResult{}, fmt.Errorf("post table %s: %w", table, err)
			}
			result.Sent += int64(len(batch))
			result.Accepted += int64(reply.Accepted)
			result.Failed += int64(reply.Failed)
			result.Batches++
			batch = batch[:0]
		}
	}
	if err := scanner.Err(); err != nil {
		return TableResult{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(batch) > 0 {
		reply, err := postBatch(ctx, opts, batch)
		if err != nil {
			return TableResult{}, fmt.Errorf("post table %s: %w", table, err)
		}
		result.Sent += int64(len(batch))
		result.Accepted += int64(reply.Accepted)
		result.Failed += int64(reply.Failed)
		result.Batches++
	}
	return result, nil
}

func postBatch(ctx context.Context, opts Options, records []json.RawMessage) (ingestResponse, error) {
	body, err := json.Marshal(map[string][]json.RawMessage{"records": records})
	if err != nil {
		return ingestResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.TargetURL, bytes.NewReader(body))
	if err != nil {
		return ingestResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := opts.HTTPClient.Do(request)
	if err != nil {
		return ingestResponse{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return ingestResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ingestResponse{}, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var reply ingestResponse
	if err := json.Unmarshal(responseBody, &reply); err != nil {
		return ingestResponse{}, fmt.Errorf("decode loader response: %w", err)
	}
	if reply.Failed > 0 {
		return reply, fmt.Errorf("loader failed %d record(s): %s", reply.Failed, strings.Join(reply.Errors, "; "))
	}
	return reply, nil
}

func postCommit(ctx context.Context, opts Options) error {
	if opts.CommitURL == "" {
		return fmt.Errorf("commit requested but no commit URL is configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.CommitURL, nil)
	if err != nil {
		return err
	}
	response, err := opts.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("commit HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
