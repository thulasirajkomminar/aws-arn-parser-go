// Package api exposes the ARN parser as a serverless HTTP handler.
//
// It is consumed by Vercel's Go runtime: any file under /api/*.go that exports
// a Handler with the signature func(http.ResponseWriter, *http.Request) becomes
// a serverless function.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/thulasirajkomminar/aws-arn-parser-go/arn"
)

const (
	paramARN     = "arn"
	paramStrict  = "strict"
	strictOffVal = "false"

	contentTypeJSON = "application/json"

	warnUnknownService   = "service not in this library's ruleset; only structural validation applied"
	warnMatchAllResource = "resource is '*' (IAM match-all); no per-resource template matched"
)

type response struct {
	Partition string `json:"partition"`
	Service   string `json:"service"`
	Region    string `json:"region"`
	AccountID string `json:"accountId"`
	Resource  string `json:"resource"`
	Warning   string `json:"warning,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
	Value string `json:"value,omitempty"`
}

// Handler is the Vercel-compatible HTTP handler for ARN parsing.
//
// GET /parse-arn?arn={ARN}&strict={true|false} returns either a parsed [arn.ARN]
// (with a warning when strict is on and the service is unknown) or an error
// response with field-level detail.
func Handler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)

	if !methodAllowed(w, r) {
		return
	}

	arnStr := r.URL.Query().Get(paramARN)
	if arnStr == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "arn parameter is required"})

		return
	}

	strict := r.URL.Query().Get(paramStrict) != strictOffVal

	parser := arn.Parse
	if strict {
		parser = arn.ParseStrict
	}

	parsed, err := parser(arnStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, buildErrorResponse(err))

		return
	}

	resp := buildSuccessResponse(&parsed)
	if strict {
		resp.Warning = strictWarning(&parsed)
	}

	writeJSON(w, http.StatusOK, resp)
}

func strictWarning(parsed *arn.ARN) string {
	if !arn.KnownService(parsed.Service) {
		return warnUnknownService
	}

	if parsed.Resource == "*" {
		return warnMatchAllResource
	}

	return ""
}

func methodAllowed(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)

		return false
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})

		return false
	}

	return true
}

func buildErrorResponse(err error) errorResponse {
	resp := errorResponse{Error: err.Error()}

	ve, ok := errors.AsType[*arn.ValidationError](err)
	if ok {
		resp.Field = ve.Field
		resp.Value = ve.Value
	}

	return resp
}

func buildSuccessResponse(parsed *arn.ARN) response {
	return response{
		Partition: parsed.Partition,
		Service:   parsed.Service,
		Region:    parsed.Region,
		AccountID: parsed.AccountID,
		Resource:  parsed.Resource,
	}
}

func setHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.WriteHeader(status)

	err := json.NewEncoder(w).Encode(body)
	if err != nil {
		log.Printf("encode response: %v", err)
	}
}
