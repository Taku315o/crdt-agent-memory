package memsync

import (
	"context"
	"net/http"
	"sort"
)

const TransportHTTPDev = "http-dev"

type TransportClient interface {
	Handshake(ctx context.Context, req HandshakeRequest) (HandshakeResponse, error)
	Pull(ctx context.Context, req PullRequest) (Batch, error)
	Apply(ctx context.Context, req ApplyRequest) error
}

type TransportServer interface {
	Handler() http.Handler
}

func AllowedNamespaceSet(namespaces []string) map[string]struct{} {
	out := make(map[string]struct{}, len(namespaces))
	for _, namespace := range namespaces {
		out[namespace] = struct{}{}
	}
	return out
}

func IntersectNamespaces(left []string, right []string) []string {
	rightSet := AllowedNamespaceSet(right)
	var out []string
	for _, namespace := range left {
		if _, ok := rightSet[namespace]; ok {
			out = append(out, namespace)
		}
	}
	sort.Strings(out)
	return out
}
