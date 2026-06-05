package utils

import "context"

// Context keys
type requestContextKey struct{}
type apiKeyContextKey struct{}

// WithRequestContext attaches a RequestContext to the context
func WithRequestContext(ctx context.Context, rc *RequestContext) context.Context {
	return context.WithValue(ctx, requestContextKey{}, rc)
}

// RequestContextFromCtx extracts RequestContext from context
func RequestContextFromCtx(ctx context.Context) *RequestContext {
	if rc, ok := ctx.Value(requestContextKey{}).(*RequestContext); ok {
		return rc
	}
	return nil
}

// WithAPIKey stores the client's Bearer token in context.
// The gateway extracts this from the incoming request's Authorization header
// and passes it through to the provider, which uses it to call the third-party API.
func WithAPIKey(ctx context.Context, apiKey string) context.Context {
	return context.WithValue(ctx, apiKeyContextKey{}, apiKey)
}

// APIKeyFromCtx retrieves the API key from context.
// Used by providers to get the client's key for authenticating with the third-party API.
func APIKeyFromCtx(ctx context.Context) string {
	if key, ok := ctx.Value(apiKeyContextKey{}).(string); ok {
		return key
	}
	return ""
}
