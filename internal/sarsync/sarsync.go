// Command sarsync fetches the AWS Service Reference (SAR) data and emits
// arn/services_generated.go.
//
// SAR is published at https://servicereference.us-east-1.amazonaws.com/. The
// root endpoint returns an index of services; each service has its own JSON
// document listing resource types and their ARN format templates.
//
// The generator translates each template (e.g.
// "arn:${Partition}:lambda:${Region}:${Account}:function:${FunctionName}")
// into a Go regular expression that the runtime can match against the parsed
// "<region>:<account>:<resource>" tail of an ARN.
//
// Run:
//
//	go run ./internal/sarsync
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	defaultIndexURL    = "https://servicereference.us-east-1.amazonaws.com/"
	defaultOutputPath  = "arn/services_generated.go"
	defaultHTTPTimeout = 30 * time.Second
	maxRetries         = 3
	retryBaseDelay     = 500 * time.Millisecond
	maxConcurrent      = 8

	dirPerm  os.FileMode = 0o750
	filePerm os.FileMode = 0o600

	arnSectionCount = 6
	serviceIdx      = 2
	regionIdx       = 3
	accountIdx      = 4
	resourceIdx     = 5

	exitErr = 1
)

var (
	errIndexEmpty      = errors.New("sar index is empty")
	errNotARNTemplate  = errors.New("not an ARN template")
	errBadSectionCount = errors.New("ARN template has wrong section count")
	errVariableService = errors.New("service section must be a literal")
	errBadHTTPStatus   = errors.New("unexpected HTTP status")
)

func main() {
	output := flag.String("o", defaultOutputPath, "output Go source path")
	indexURL := flag.String("index", defaultIndexURL, "SAR index URL")

	flag.Parse()

	os.Exit(realMain(*indexURL, *output))
}

func realMain(indexURL, output string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := run(ctx, options{IndexURL: indexURL, OutputPath: output})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return exitErr
	}

	return 0
}

// options configures run.
type options struct {
	IndexURL   string
	OutputPath string
	HTTPClient *http.Client
	Logger     *log.Logger
}

func (o *options) defaults() {
	if o.IndexURL == "" {
		o.IndexURL = defaultIndexURL
	}

	if o.OutputPath == "" {
		o.OutputPath = defaultOutputPath
	}

	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}

	if o.Logger == nil {
		o.Logger = log.New(os.Stderr, "sarsync: ", log.LstdFlags)
	}
}

// indexEntry is one row of the SAR index. Field names use camelCase to match
// the AWS-published wire format.
type indexEntry struct {
	Service  string `json:"service"`
	URL      string `json:"url"`
	Modified int64  `json:"modified"`
}

// serviceDoc is the per-service SAR document. AWS publishes these with
// PascalCase keys, so the tags intentionally do not follow Go camelCase
// convention.
//
//nolint:tagliatelle // AWS wire format uses PascalCase keys
type serviceDoc struct {
	Name      string     `json:"Name"`
	Version   string     `json:"Version"`
	Resources []resource `json:"Resources"`
}

//nolint:tagliatelle // AWS wire format uses PascalCase keys
type resource struct {
	Name       string   `json:"Name"`
	ARNFormats []string `json:"ARNFormats"`
}

// rule is the internal representation of a service's ARN patterns before codegen.
type rule struct {
	service  string
	patterns []string
}

func run(ctx context.Context, opts options) error {
	opts.defaults()

	opts.Logger.Printf("fetching index from %s", opts.IndexURL)

	index, err := fetchIndex(ctx, opts.HTTPClient, opts.IndexURL)
	if err != nil {
		return fmt.Errorf("fetch index: %w", err)
	}

	opts.Logger.Printf("index has %d entries; fetching services in parallel", len(index))

	docs := fetchAll(ctx, opts.HTTPClient, index, opts.Logger)

	rules := buildRules(docs, opts.Logger)
	opts.Logger.Printf("built rules for %d services", len(rules))

	src, err := generate(rules)
	if err != nil {
		return fmt.Errorf("generate source: %w", err)
	}

	err = writeFile(opts.OutputPath, src)
	if err != nil {
		return fmt.Errorf("write %s: %w", opts.OutputPath, err)
	}

	opts.Logger.Printf("wrote %s (%d bytes)", opts.OutputPath, len(src))

	return nil
}

func fetchIndex(ctx context.Context, client *http.Client, idxURL string) ([]indexEntry, error) {
	body, err := getWithRetry(ctx, client, idxURL)
	if err != nil {
		return nil, err
	}

	var entries []indexEntry

	err = json.Unmarshal(body, &entries)
	if err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}

	if len(entries) == 0 {
		return nil, errIndexEmpty
	}

	return entries, nil
}

// fetchResult bundles a per-service download outcome for the worker pool.
type fetchResult struct {
	entry indexEntry
	doc   serviceDoc
	err   error
}

// fetchAll downloads every service document in parallel. Per-service failures
// are logged and skipped (fail-open at the service level).
func fetchAll(
	ctx context.Context,
	client *http.Client,
	index []indexEntry,
	logger *log.Logger,
) map[string]serviceDoc {
	jobs := make(chan indexEntry)
	results := make(chan fetchResult)

	spawnFetchWorkers(ctx, client, jobs, results)

	go dispatchFetchJobs(ctx, index, jobs)

	return collectFetchResults(results, len(index), logger)
}

func spawnFetchWorkers(ctx context.Context, client *http.Client, jobs chan indexEntry, results chan fetchResult) {
	for range maxConcurrent {
		go func() {
			for entry := range jobs {
				doc, err := fetchService(ctx, client, entry)
				results <- fetchResult{entry: entry, doc: doc, err: err}
			}
		}()
	}
}

func dispatchFetchJobs(ctx context.Context, index []indexEntry, jobs chan<- indexEntry) {
	defer close(jobs)

	for _, e := range index {
		select {
		case <-ctx.Done():
			return
		case jobs <- e:
		}
	}
}

func collectFetchResults(results <-chan fetchResult, expected int, logger *log.Logger) map[string]serviceDoc {
	out := make(map[string]serviceDoc, expected)

	for range expected {
		r := <-results
		if r.err != nil {
			logger.Printf("skip %s: %v", r.entry.Service, r.err)

			continue
		}

		out[r.entry.Service] = r.doc
	}

	return out
}

func fetchService(ctx context.Context, client *http.Client, entry indexEntry) (serviceDoc, error) {
	body, err := getWithRetry(ctx, client, entry.URL)
	if err != nil {
		return serviceDoc{}, err
	}

	var doc serviceDoc

	err = json.Unmarshal(body, &doc)
	if err != nil {
		return serviceDoc{}, fmt.Errorf("decode %s: %w", entry.Service, err)
	}

	return doc, nil
}

func getWithRetry(ctx context.Context, client *http.Client, target string) ([]byte, error) {
	_, parseErr := url.Parse(target)
	if parseErr != nil {
		return nil, fmt.Errorf("invalid url %q: %w", target, parseErr)
	}

	var lastErr error

	for attempt := range maxRetries {
		if attempt > 0 {
			err := waitBackoff(ctx, attempt)
			if err != nil {
				return nil, err
			}
		}

		body, err := doGet(ctx, client, target)
		if err == nil {
			return body, nil
		}

		lastErr = err
	}

	return nil, fmt.Errorf("after %d attempts: %w", maxRetries, lastErr)
}

func waitBackoff(ctx context.Context, attempt int) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("context cancelled during backoff: %w", ctx.Err())
	case <-time.After(retryBaseDelay << attempt):
		return nil
	}
}

func doGet(ctx context.Context, client *http.Client, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", target, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: %w (%d)", target, errBadHTTPStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body of %s: %w", target, err)
	}

	return body, nil
}

// buildRules collapses serviceDoc data into translated regex patterns. A
// service may appear under multiple file names (e.g. s3-object-lambda inside
// s3.json); we key by the service token in each ARN, not by the file name.
func buildRules(docs map[string]serviceDoc, logger *log.Logger) []rule {
	byService := collectPatterns(docs, logger)
	rules := flattenAndSort(byService)

	return rules
}

func collectPatterns(docs map[string]serviceDoc, logger *log.Logger) map[string]map[string]struct{} {
	byService := map[string]map[string]struct{}{}

	for _, doc := range docs {
		ingestDoc(doc, byService, logger)
	}

	return byService
}

func ingestDoc(doc serviceDoc, byService map[string]map[string]struct{}, logger *log.Logger) {
	for _, res := range doc.Resources {
		for _, raw := range res.ARNFormats {
			t, err := translateARNFormat(raw)
			if err != nil {
				logger.Printf("skip ARN format %q (%s): %v", raw, doc.Name, err)

				continue
			}

			if _, ok := byService[t.service]; !ok {
				byService[t.service] = map[string]struct{}{}
			}

			byService[t.service][t.pattern] = struct{}{}
		}
	}
}

func flattenAndSort(byService map[string]map[string]struct{}) []rule {
	out := make([]rule, 0, len(byService))

	for svc, set := range byService {
		patterns := make([]string, 0, len(set))
		for p := range set {
			patterns = append(patterns, p)
		}

		slices.Sort(patterns)
		out = append(out, rule{service: svc, patterns: patterns})
	}

	slices.SortFunc(out, func(a, b rule) int {
		return strings.Compare(a.service, b.service)
	})

	return out
}

// translated is the result of converting an AWS ARN template into runtime
// matchable form.
type translated struct {
	service string
	pattern string
}

// translateARNFormat converts an AWS ARN template into a regex that matches
// the "<region>:<account>:<resource>" tail.
//
//	"arn:${Partition}:lambda:${Region}:${Account}:function:${FunctionName}"
//
// becomes
//
//	service = "lambda"
//	pattern = `^[^:]+:[^:]+:function:[^:]+$`
//
// Variables become `[^:]+`. Literal characters are regex-escaped. An empty
// section (e.g. the region in "arn:aws:s3:::bucket") means the section must
// also be empty in real ARNs.
func translateARNFormat(raw string) (translated, error) {
	if !strings.HasPrefix(raw, "arn:") {
		return translated{}, errNotARNTemplate
	}

	parts := strings.SplitN(raw, ":", arnSectionCount)
	if len(parts) != arnSectionCount {
		return translated{}, fmt.Errorf("%w: got %d sections", errBadSectionCount, len(parts))
	}

	svcSection := parts[serviceIdx]
	if svcSection == "" || strings.Contains(svcSection, "${") {
		return translated{}, fmt.Errorf("%w: %q", errVariableService, svcSection)
	}

	regionRe := translateSection(parts[regionIdx])
	accountRe := translateSection(parts[accountIdx])
	resourceRe := translateSection(parts[resourceIdx])

	pattern := "^" + regionRe + ":" + accountRe + ":" + resourceRe + "$"

	_, err := regexp.Compile(pattern)
	if err != nil {
		return translated{}, fmt.Errorf("invalid generated regex %q: %w", pattern, err)
	}

	return translated{service: svcSection, pattern: pattern}, nil
}

// translateSection converts one ARN section template into a regex fragment.
// Each ${Var} placeholder becomes [^:]+; literal characters between
// placeholders are regex-escaped. An empty section (e.g. the region in
// "arn:aws:s3:::bucket") stays empty in the regex.
func translateSection(section string) string {
	if section == "" {
		return ""
	}

	var b strings.Builder

	last := 0
	for _, loc := range varRe.FindAllStringIndex(section, -1) {
		b.WriteString(regexp.QuoteMeta(section[last:loc[0]]))
		b.WriteString(`[^:]+`)

		last = loc[1]
	}

	b.WriteString(regexp.QuoteMeta(section[last:]))

	return b.String()
}

var varRe = regexp.MustCompile(`\$\{[^}]+\}`)

func writeFile(path string, src []byte) error {
	err := os.MkdirAll(filepath.Dir(path), dirPerm)
	if err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	err = os.WriteFile(path, src, filePerm)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}
