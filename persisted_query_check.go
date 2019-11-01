package handler

import (
	"errors"
)

type CacheEntry struct {
	operationName string
	query         string
	sha256Hash    string
	version       float64
}

var cache = make(map[string]CacheEntry)

func persistedQueryCheck(opts *RequestOptions) (*RequestOptions, error) {
	if opts.Extensions == nil {
		return opts, nil
	}

	persistedQuery := opts.Extensions["persistedQuery"]

	if persistedQuery == nil {
		return opts, nil
	}

	values := persistedQuery.(map[string]interface{})

	sha := values["sha256Hash"].(string)

	if sha == "" {
		return opts, nil
	}

	opts.HasPersistedParams = true

	if opts.Query == "" {
		cachedValue := cache[sha]
		if cachedValue.query == "" {
			return nil, errors.New("{\"errors\":[{\"message\":\"PersistedQueryNotFound\",\"extensions\":{\"code\":\"PERSISTED_QUERY_NOT_FOUND\"}}]}")
		}
		opts.OperationName = cachedValue.operationName
		opts.Query = cachedValue.query
		opts.Persisted = true
		return opts, nil
	} else if opts.Query != "" {
		cache[sha] = CacheEntry{
			operationName: opts.OperationName,
			query:         opts.Query,
			sha256Hash:    sha,
			version:       values["version"].(float64),
		}
	}

	return opts, nil
}
