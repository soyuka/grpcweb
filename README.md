# gRPC-Web Protocol Library for Go

This repository contains a minimal, zero-dependency Go library for translating the gRPC-Web protocol to native gRPC. It is designed to be a lightweight and maintainable core for integrating gRPC-Web support into Go-based HTTP servers and proxies, such as Caddy.

This library was created to provide a shared core for projects like [dunglas/frankenphp-grpc](https://github.com/dunglas/frankenphp-grpc) and [mholt/caddy-grpc-web](https://github.com/mholt/caddy-grpc-web).

Largly inspired from:
- [envoy proxy](https://github.com/envoyproxy/envoy/blob/c811552b94d0d4189de710113d5f081f6c952e5b/source/extensions/filters/http/grpc_web/grpc_web_filter.cc)
- [improbable-eng/grpc-web](https://github.com/improbable-eng/grpc-web)

## Usage

Wrap the `grpcweb.Handler` around your existing gRPC backend handler (e.g., a reverse proxy).

### Example within a Caddy Module:

package mymodule

import (
    "net/http"
    "net/http/httputil"
    "net/url"

    "github.com/caddyserver/caddy/v2/modules/caddyhttp"
    "github.com/soyuka/grpcweb"
)

// MyHandler is a Caddy HTTP handler.
type MyHandler struct {
    // ... other fields
}

func (h *MyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
    // This is just an example; in reality, the `next` handler
    // would be the reverse_proxy to the gRPC server.
    backendURL, _ := url.Parse("http://localhost:50051")
    proxy := httputil.NewSingleHostReverseProxy(backendURL)

    // Wrap the proxy with the gRPC-Web handler.
    grpcwebWrapper := &grpcweb.Handler{
        GRPCServer: proxy,
    }

    // The wrapper will handle the request if it's gRPC-Web.
    grpcwebWrapper.ServeHTTP(w, r)

    return nil
}

See also [./testdata](/testdata/README.md).
