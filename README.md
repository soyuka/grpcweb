# gRPC-Web Protocol Library for Go

This repository contains a minimal, zero-dependency Go library for translating the gRPC-Web protocol to native gRPC. It is designed to be a lightweight and maintainable core for integrating gRPC-Web support into Go-based HTTP servers and proxies, such as Caddy.

This library was created to provide a shared core for projects like [dunglas/frankenphp-grpc](https://github.com/dunglas/frankenphp-grpc) and [mholt/caddy-grpc-web](https://github.com/mholt/caddy-grpc-web).

Largly inspired from:
- [envoy proxy](https://github.com/envoyproxy/envoy/blob/c811552b94d0d4189de710113d5f081f6c952e5b/source/extensions/filters/http/grpc_web/grpc_web_filter.cc)
- [improbable-eng/grpc-web](https://github.com/improbable-eng/grpc-web)

## Usage

The library provides a standard http.Handler that can be used as middleware to wrap your gRPC backend (e.g., a reverse proxy). It will automatically translate gRPC-Web requests on the fly.

### Example within a Caddy Module:

This example shows how to create a Caddy middleware that uses this library to enable gRPC-Web for a `reverse_proxy`.

```go
package mymodule

import (
    "net/http"

    "github.com/caddyserver/caddy/v2/modules/caddyhttp"
    "github.com/soyuka/grpcweb"
)

// MyHandler is a Caddy HTTP handler that enables gRPC-Web.
type MyHandler struct {}

func (h *MyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
    // Check if the request is a gRPC-Web request.
    if grpcweb.IsGRPCWebRequest(r) {
        // If it is, wrap the next handler (the reverse_proxy to the gRPC server)
        // with our translator handler.
        grpcwebWrapper := &grpcweb.Handler{
            GRPCServer: next,
        }
        grpcwebWrapper.ServeHTTP(w, r)
        return nil // The request has been handled.
    }

    // If it's not a gRPC-Web request, pass it through unmodified.
    return next.ServeHTTP(w, r)
}
```

This pattern ensures that only gRPC-Web requests are processed by our translator, while native gRPC and other HTTP requests are passed through untouched.

See also [./testdata](/testdata/README.md).
