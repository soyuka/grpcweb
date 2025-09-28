// Package grpcweb provides a generic HTTP handler for gRPC-Web.
package grpcweb

import (
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Handler is an http.Handler that wraps a gRPC server and translates
// gRPC-Web requests to gRPC.
type Handler struct {
	// The gRPC server to handle requests
	GRPCServer http.Handler
}

// ServeHTTP implements the http.Handler interface.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !IsGRPCWebRequest(r) {
		// Fallback to the underlying gRPC server for non-gRPC-Web requests,
		// or you could return an error/next handler.
		h.GRPCServer.ServeHTTP(w, r)
		return
	}

	grpcRequest, err := newGrpcRequest(r)
	if err != nil {
		writeErrorResponse(w, r, status.New(codes.InvalidArgument, err.Error()))
		return
	}

	isText := IsTextRequest(r)
	streamingWriter := NewStreamingResponseWriter(w, isText)
	defer func() {
		// It's safe to ignore the error here as the stream is likely closed.
		_ = streamingWriter.Finish()
	}()

	h.GRPCServer.ServeHTTP(streamingWriter, grpcRequest)
}

// newGrpcRequest converts an incoming gRPC-Web request to a native gRPC request.
func newGrpcRequest(r *http.Request) (*http.Request, error) {
	isTextEncoded := strings.HasSuffix(r.Header.Get("Content-Type"), "-text")
	bodyReader := NewFrameReader(r.Body, isTextEncoded)

	grpcRequest, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), bodyReader)
	if err != nil {
		return nil, err
	}

	// Copy essential headers
	for key, values := range r.Header {
		lowerKey := strings.ToLower(key)
		// These headers are either hop-by-hop or handled by the gRPC-Web protocol itself.
		if lowerKey == "user-agent" || lowerKey == "authorization" || strings.HasPrefix(lowerKey, "x-") {
			grpcRequest.Header[key] = values
		}
	}

	// Set headers to make it a valid gRPC request
	grpcRequest.Header.Set("Content-Type", ContentTypeGRPC)
	grpcRequest.Header.Set("TE", "trailers")
	grpcRequest.Proto = "HTTP/2.0"
	grpcRequest.ProtoMajor = 2
	grpcRequest.ProtoMinor = 0
	grpcRequest.ContentLength = -1 // Streaming request

	return grpcRequest, nil
}

// writeErrorResponse writes a gRPC-Web formatted error.
func writeErrorResponse(w http.ResponseWriter, r *http.Request, st *status.Status) {
	isText := IsTextRequest(r)
	sw := NewStreamingResponseWriter(w, isText)
	sw.trailers.Set("grpc-status", strconv.Itoa(int(st.Code())))
	sw.trailers.Set("grpc-message", st.Message())
	_ = sw.Finish() // Best effort
}
