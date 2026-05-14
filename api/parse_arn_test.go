package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type handlerCase struct {
	name              string
	method            string
	arn               string
	strict            string
	omitARN           bool
	expectedStatus    int
	expectedRegion    string
	expectedAccountID string
	expectedWarning   bool
	expectedField     string
}

func TestHandler_Success(t *testing.T) {
	t.Parallel()

	cases := []handlerCase{
		{
			name:           "Valid S3 ARN (strict default)",
			method:         http.MethodGet,
			arn:            "arn:aws:s3:::my-bucket/folder/file.txt",
			expectedStatus: http.StatusOK,
		},
		{
			name:              "Valid Lambda ARN",
			method:            http.MethodGet,
			arn:               "arn:aws:lambda:us-east-1:123456789012:function:my-func",
			expectedStatus:    http.StatusOK,
			expectedRegion:    "us-east-1",
			expectedAccountID: "123456789012",
		},
		{
			// IAM policy-style "match-all" resource ('*') is accepted per AWS docs.
			// A warning notes that no per-resource template matched.
			name:              "IAM match-all resource accepted with warning",
			method:            http.MethodGet,
			arn:               "arn:aws:qbusiness:*:123456789012:*",
			expectedStatus:    http.StatusOK,
			expectedRegion:    "*",
			expectedAccountID: "123456789012",
			expectedWarning:   true,
		},
		{
			name:              "Unknown service in strict gets warning",
			method:            http.MethodGet,
			arn:               "arn:aws:newservice:us-east-1:123456789012:thing/foo",
			expectedStatus:    http.StatusOK,
			expectedRegion:    "us-east-1",
			expectedAccountID: "123456789012",
			expectedWarning:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := runHandler(t, tc)
			assertJSONContentType(t, w)

			var parsed response

			err := json.Unmarshal(w.Body.Bytes(), &parsed)
			if err != nil {
				t.Fatalf("failed to parse JSON response: %v", err)
			}

			assertSuccess(t, tc, parsed)
		})
	}
}

func TestHandler_Errors(t *testing.T) {
	t.Parallel()

	cases := []handlerCase{
		{
			// Without a wildcard the typo is caught: "fonction" doesn't match
			// AWS's Lambda template.
			name:           "Lambda typo rejected in strict",
			method:         http.MethodGet,
			arn:            "arn:aws:lambda:us-east-1:123456789012:fonction:my-func",
			expectedStatus: http.StatusBadRequest,
			expectedField:  "resource",
		},
		{
			name:           "Bad partition rejected",
			method:         http.MethodGet,
			arn:            "arn:aws-fake:s3:::bucket",
			expectedStatus: http.StatusBadRequest,
			expectedField:  "partition",
		},
		{
			name:           "Invalid ARN format",
			method:         http.MethodGet,
			arn:            "invalid-arn",
			expectedStatus: http.StatusBadRequest,
			expectedField:  "format",
		},
		{
			name:           "Missing ARN parameter",
			method:         http.MethodGet,
			omitARN:        true,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := runHandler(t, tc)
			assertJSONContentType(t, w)
			assertErrorBody(t, tc, w.Body.Bytes())
		})
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	w := runHandler(t, handlerCase{
		method:         http.MethodPost,
		arn:            "arn:aws:s3:::my-bucket",
		expectedStatus: http.StatusMethodNotAllowed,
	})

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestHandler_OptionsPreflight(t *testing.T) {
	t.Parallel()

	w := runHandler(t, handlerCase{
		method:         http.MethodOptions,
		omitARN:        true,
		expectedStatus: http.StatusNoContent,
	})

	if w.Code != http.StatusNoContent {
		t.Errorf("expected %d, got %d", http.StatusNoContent, w.Code)
	}
}

func TestCORSHeaders(t *testing.T) {
	t.Parallel()

	w := runHandler(t, handlerCase{
		method: http.MethodGet,
		arn:    "arn:aws:s3:::my-bucket",
	})

	got := w.Header().Get("Access-Control-Allow-Origin")
	if got != "*" {
		t.Errorf("expected CORS origin '*', got %q", got)
	}
}

func runHandler(t *testing.T, c handlerCase) *httptest.ResponseRecorder {
	t.Helper()

	target := "/"
	q := url.Values{}

	if !c.omitARN {
		q.Set(paramARN, c.arn)
	}

	if c.strict != "" {
		q.Set(paramStrict, c.strict)
	}

	if len(q) > 0 {
		target = "/?" + q.Encode()
	}

	req := httptest.NewRequestWithContext(t.Context(), c.method, target, nil)
	w := httptest.NewRecorder()

	Handler(w, req)

	if c.expectedStatus != 0 && w.Code != c.expectedStatus {
		t.Errorf("expected status %d, got %d; body=%s", c.expectedStatus, w.Code, w.Body.String())
	}

	return w
}

func assertJSONContentType(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func assertSuccess(t *testing.T, tc handlerCase, parsed response) {
	t.Helper()

	if parsed.Region != tc.expectedRegion {
		t.Errorf("expected region %q, got %q", tc.expectedRegion, parsed.Region)
	}

	if parsed.AccountID != tc.expectedAccountID {
		t.Errorf("expected accountId %q, got %q", tc.expectedAccountID, parsed.AccountID)
	}

	if tc.expectedWarning && parsed.Warning == "" {
		t.Errorf("expected warning, got none")
	}

	if !tc.expectedWarning && parsed.Warning != "" {
		t.Errorf("unexpected warning: %s", parsed.Warning)
	}
}

func assertErrorBody(t *testing.T, tc handlerCase, body []byte) {
	t.Helper()

	var errResp map[string]string

	err := json.Unmarshal(body, &errResp)
	if err != nil {
		t.Errorf("error response not valid JSON: %v; body=%s", err, body)
	}

	_, hasError := errResp["error"]
	if !hasError {
		t.Errorf("error response missing 'error' field: %s", body)
	}

	if tc.expectedField != "" && errResp["field"] != tc.expectedField {
		t.Errorf("expected field %q, got %q (body: %s)", tc.expectedField, errResp["field"], body)
	}
}
