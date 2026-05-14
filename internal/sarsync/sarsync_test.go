package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTranslateARNFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		raw        string
		wantSvc    string
		wantRegex  string
		matches    []string
		notMatches []string
	}{
		{
			name:      "lambda function",
			raw:       "arn:${Partition}:lambda:${Region}:${Account}:function:${FunctionName}",
			wantSvc:   "lambda",
			wantRegex: `^[^:]+:[^:]+:function:[^:]+$`,
			matches: []string{
				"us-east-1:123456789012:function:my-func",
			},
			notMatches: []string{
				"us-east-1:123456789012:functi*:my-func",
				"us-east-1:123456789012:function:",
				":123456789012:function:f", // empty region not allowed (template requires ${Region})
				"us-east-1::function:f",    // empty account not allowed
			},
		},
		{
			name:      "s3 bucket (empty region+account)",
			raw:       "arn:${Partition}:s3:::${BucketName}",
			wantSvc:   "s3",
			wantRegex: `^::[^:]+$`,
			matches: []string{
				"::my-bucket",
				"::My-Bucket-Or-Anything", // SAR is permissive about bucket name chars
			},
			notMatches: []string{
				"us-east-1:123:my-bucket",
				"::",
			},
		},
		{
			name:      "s3 access point",
			raw:       "arn:${Partition}:s3:${Region}:${Account}:accesspoint/${AccessPointName}",
			wantSvc:   "s3",
			wantRegex: `^[^:]+:[^:]+:accesspoint/[^:]+$`,
			matches: []string{
				"us-east-1:123:accesspoint/my-ap",
			},
			notMatches: []string{
				"us-east-1:123:bucket/my-ap",
				"::accesspoint/my-ap", // empty region+account not allowed for access points
			},
		},
		{
			name:      "iam user (empty region)",
			raw:       "arn:${Partition}:iam::${Account}:user/${UserNameWithPath}",
			wantSvc:   "iam",
			wantRegex: `^:[^:]+:user/[^:]+$`,
			matches: []string{
				":123:user/jane",
			},
			notMatches: []string{
				"us-east-1:123:user/jane", // region must be empty
				":123:thing/jane",         // wrong resource type
				"::user/jane",             // account required (template has ${Account})
			},
		},
		{
			name:      "lambda layer with version (multi-section resource)",
			raw:       "arn:${Partition}:lambda:${Region}:${Account}:layer:${LayerName}:${LayerVersion}",
			wantSvc:   "lambda",
			wantRegex: `^[^:]+:[^:]+:layer:[^:]+:[^:]+$`,
			matches: []string{
				"us-east-1:123:layer:my-layer:1",
			},
			notMatches: []string{
				"us-east-1:123:layer:my-layer", // missing version
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tr, err := translateARNFormat(tc.raw)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}

			if tr.service != tc.wantSvc {
				t.Errorf("service = %q, want %q", tr.service, tc.wantSvc)
			}

			if tr.pattern != tc.wantRegex {
				t.Errorf("pattern = %q, want %q", tr.pattern, tc.wantRegex)
			}

			re := regexp.MustCompile(tr.pattern)
			for _, m := range tc.matches {
				if !re.MatchString(m) {
					t.Errorf("pattern %q should match %q but didn't", tr.pattern, m)
				}
			}

			for _, m := range tc.notMatches {
				if re.MatchString(m) {
					t.Errorf("pattern %q should NOT match %q", tr.pattern, m)
				}
			}
		})
	}
}

func TestTranslateARNFormat_Rejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"missing arn prefix", "not-an-arn:foo:bar"},
		{"too few sections", "arn:aws:s3"},
		{"variable service section", "arn:${Partition}:${Service}:${Region}:${Account}:foo"},
		{"empty service section", "arn:${Partition}::${Region}:${Account}:foo"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := translateARNFormat(tc.raw)
			if err == nil {
				t.Errorf("expected error for %q", tc.raw)
			}
		})
	}
}

// TestRun_Fixtures spins up a local HTTP server that serves the bundled SAR
// fixtures, runs the generator end-to-end, and verifies the output.
func TestRun_Fixtures(t *testing.T) {
	t.Parallel()

	srv := newFixtureServer(t)
	defer srv.Close()

	outPath := filepath.Join(t.TempDir(), "services_generated.go")

	err := run(t.Context(), options{IndexURL: srv.URL + "/", OutputPath: outPath})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	assertGeneratedContent(t, outPath)
}

func newFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			serveIndex(t, w, r)

			return
		}

		base := filepath.Base(r.URL.Path)
		name := strings.TrimSuffix(base, ".json")

		http.ServeFile(w, r, filepath.Join("testdata", name+".json"))
	})

	return httptest.NewServer(mux)
}

func serveIndex(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "index.json"))
	if err != nil {
		t.Fatalf("read index fixture: %v", err)
	}

	var entries []indexEntry

	err = json.Unmarshal(data, &entries)
	if err != nil {
		t.Fatalf("decode index fixture: %v", err)
	}

	rewritten := entries[:0]

	for _, e := range entries {
		if !hasFixture(e.Service) {
			continue
		}

		e.URL = "http://" + r.Host + "/v1/" + e.Service + "/" + e.Service + ".json"
		rewritten = append(rewritten, e)
	}

	err = json.NewEncoder(w).Encode(rewritten)
	if err != nil {
		t.Fatalf("encode index: %v", err)
	}
}

func assertGeneratedContent(t *testing.T, outPath string) {
	t.Helper()

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}

	out := string(data)

	expected := []string{
		"package arn",
		"DO NOT EDIT",
		`"lambda":`,
		`"s3":`,
		`"iam":`,
		"arnFormats:",
		"regexp.MustCompile",
		"function:[^:]+",
	}
	for _, must := range expected {
		if !strings.Contains(out, must) {
			t.Errorf("generated file missing expected substring %q", must)
		}
	}
}

func hasFixture(svc string) bool {
	_, err := os.Stat(filepath.Join("testdata", svc+".json"))

	return err == nil
}
