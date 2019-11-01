package handler

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/graphql-go/graphql"

	"context"

	"github.com/graphql-go/graphql/gqlerrors"
)

const (
	ContentTypeJSON           = "application/json"
	ContentTypeGraphQL        = "application/graphql"
	ContentTypeFormURLEncoded = "application/x-www-form-urlencoded"
)

type ResultCallbackFn func(ctx context.Context, params *graphql.Params, result *graphql.Result, responseBody []byte)

type Handler struct {
	Schema           *graphql.Schema
	pretty           bool
	graphiql         bool
	playground       bool
	rootObjectFn     RootObjectFn
	resultCallbackFn ResultCallbackFn
	formatErrorFn    func(err error) gqlerrors.FormattedError
}

type RequestOptions struct {
	Query              string                 `json:"query" url:"query" schema:"query"`
	Variables          map[string]interface{} `json:"variables" url:"variables" schema:"variables"`
	OperationName      string                 `json:"operationName" url:"operationName" schema:"operationName"`
	Extensions         map[string]interface{} `json:"extensions" url:"extensions" schema:"extensions"`
	Persisted          bool
	HasPersistedParams bool
}

// a workaround for getting`variables` as a JSON string
type requestOptionsCompatibility struct {
	Query         string                 `json:"query" url:"query" schema:"query"`
	Variables     string                 `json:"variables" url:"variables" schema:"variables"`
	OperationName string                 `json:"operationName" url:"operationName" schema:"operationName"`
	Extensions    map[string]interface{} `json:"extensions" url:"extensions" schema:"extensions"`
}

func getFromForm(values url.Values) *RequestOptions {
	query := values.Get("query")
	variablesStr := values.Get("variables")
	extensionsStr := values.Get("extensions")

	variables := make(map[string]interface{}, len(values))
	json.Unmarshal([]byte(variablesStr), &variables)

	extensions := make(map[string]interface{}, len(values))
	json.Unmarshal([]byte(extensionsStr), &extensions)

	return &RequestOptions{
		Query:         query,
		Variables:     variables,
		OperationName: values.Get("operationName"),
		Extensions:    extensions,
	}
}

// RequestOptions Parses a http.Request into GraphQL request options struct
func NewRequestOptions(r *http.Request) *RequestOptions {

	reqOpt := getFromForm(r.URL.Query())

	if r.Method != "POST" && reqOpt != nil {
		return reqOpt
	}

	if r.Method != http.MethodPost {
		return &RequestOptions{}
	}

	if r.Body == nil {
		return &RequestOptions{}
	}

	// TODO: improve Content-Type handling
	contentTypeStr := r.Header.Get("Content-Type")
	contentTypeTokens := strings.Split(contentTypeStr, ";")
	contentType := contentTypeTokens[0]

	switch contentType {
	case ContentTypeGraphQL:
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return &RequestOptions{}
		}
		return &RequestOptions{
			Query: string(body),
		}
	case ContentTypeFormURLEncoded:
		if err := r.ParseForm(); err != nil {
			return &RequestOptions{}
		}

		if reqOpt := getFromForm(r.PostForm); reqOpt != nil {
			return reqOpt
		}

		return &RequestOptions{}

	case ContentTypeJSON:
		fallthrough
	default:
		var opts RequestOptions
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return &opts
		}
		err = json.Unmarshal(body, &opts)
		if err != nil {
			// Probably `variables` was sent as a string instead of an object.
			// So, we try to be polite and try to parse that as a JSON string
			var optsCompatible requestOptionsCompatibility
			json.Unmarshal(body, &optsCompatible)
			json.Unmarshal([]byte(optsCompatible.Variables), &opts.Variables)
		}
		return &opts
	}
}

// ContextHandler provides an entrypoint into executing graphQL queries with a
// user-provided context.
func (h *Handler) ContextHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	// get query
	opts := NewRequestOptions(r)

	// persisted query implementation
	opts, err := persistedQueryCheck(opts)

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(err.Error()))
		return
	}

	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}

	// execute graphql query
	params := graphql.Params{
		Schema:         *h.Schema,
		RequestString:  opts.Query,
		VariableValues: opts.Variables,
		OperationName:  opts.OperationName,
		Context:        ctx,
	}
	if h.rootObjectFn != nil {
		params.RootObject = h.rootObjectFn(ctx, r)
	}
	result := graphql.Do(params)

	if formatErrorFn := h.formatErrorFn; formatErrorFn != nil && len(result.Errors) > 0 {
		formatted := make([]gqlerrors.FormattedError, len(result.Errors))
		for i, formattedError := range result.Errors {
			formatted[i] = formatErrorFn(formattedError.OriginalError())
		}
		result.Errors = formatted
	}

	if h.graphiql {
		acceptHeader := r.Header.Get("Accept")
		_, raw := r.URL.Query()["raw"]
		if !raw && !strings.Contains(acceptHeader, "application/json") && strings.Contains(acceptHeader, "text/html") {
			renderGraphiQL(w, params)
			return
		}
	}

	if h.playground {
		acceptHeader := r.Header.Get("Accept")
		_, raw := r.URL.Query()["raw"]
		if !raw && !strings.Contains(acceptHeader, "application/json") && strings.Contains(acceptHeader, "text/html") {
			renderPlayground(w, r)
			return
		}
	}

	// use proper JSON Header
	w.Header().Add("Content-Type", "application/json; charset=utf-8")

	var buff []byte
	if h.pretty {
		w.WriteHeader(http.StatusOK)
		buff, _ = json.MarshalIndent(result, "", "\t")

		w.Write(buff)
	} else {
		w.WriteHeader(http.StatusOK)
		buff, _ = json.Marshal(result)

		w.Write(buff)
	}

	if h.resultCallbackFn != nil {
		h.resultCallbackFn(ctx, &params, result, buff)
	}
}

// ServeHTTP provides an entrypoint into executing graphQL queries.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.ContextHandler(r.Context(), w, r)
}

// RootObjectFn allows a user to generate a RootObject per request
type RootObjectFn func(ctx context.Context, r *http.Request) map[string]interface{}

type Config struct {
	Schema           *graphql.Schema
	Pretty           bool
	GraphiQL         bool
	Playground       bool
	RootObjectFn     RootObjectFn
	ResultCallbackFn ResultCallbackFn
	FormatErrorFn    func(err error) gqlerrors.FormattedError
}

func NewConfig() *Config {
	return &Config{
		Schema:     nil,
		Pretty:     true,
		GraphiQL:   true,
		Playground: false,
	}
}

func New(p *Config) *Handler {
	if p == nil {
		p = NewConfig()
	}

	if p.Schema == nil {
		panic("undefined GraphQL schema")
	}

	return &Handler{
		Schema:           p.Schema,
		pretty:           p.Pretty,
		graphiql:         p.GraphiQL,
		playground:       p.Playground,
		rootObjectFn:     p.RootObjectFn,
		resultCallbackFn: p.ResultCallbackFn,
		formatErrorFn:    p.FormatErrorFn,
	}
}
