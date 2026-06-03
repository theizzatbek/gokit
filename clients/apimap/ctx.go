package apimap

import "context"

// endpointNameKey is the private context key under which apimap stores
// the endpoint name for the current request. Read by the apimap-level
// hook middleware so callbacks can see the kit-stable
// (client, endpoint) pair regardless of whether the request flowed
// through a per-client or per-endpoint *http.Client.
type endpointNameKey struct{}

func contextWithEndpointName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, endpointNameKey{}, name)
}

func endpointNameFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(endpointNameKey{}).(string); ok {
		return v
	}
	return ""
}
