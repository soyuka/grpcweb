// Package grpcweb provides core utilities for handling the gRPC-Web protocol.
package grpcweb

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
)

const (
	// ContentTypeGRPCWeb is the base gRPC-Web content type
	ContentTypeGRPCWeb = "application/grpc-web"
	// ContentTypeGRPCWebText is the gRPC-Web content type for base64 encoded text
	ContentTypeGRPCWebText = "application/grpc-web-text"
	// ContentTypeGRPCWebProto is the gRPC-Web content type with proto subtype
	ContentTypeGRPCWebProto = "application/grpc-web+proto"
	// ContentTypeGRPCWebTextProto is the gRPC-Web text content type with proto subtype
	ContentTypeGRPCWebTextProto = "application/grpc-web-text+proto"
	// ContentTypeGRPC is the native gRPC content type
	ContentTypeGRPC = "application/grpc"

	// Frame types
	grpcDataFrame    byte = 0x00
	grpcTrailerFrame byte = 0x80 // MSB is set
)

// FrameReader translates a gRPC-Web request body into a standard gRPC request body.
type FrameReader struct {
	source io.Reader
	buffer bytes.Buffer
}

// NewFrameReader creates a new reader that translates gRPC-Web frames.
func NewFrameReader(r io.Reader, isTextEncoded bool) io.Reader {
	var bodyReader io.Reader = r
	if isTextEncoded {
		bodyReader = base64.NewDecoder(base64.StdEncoding, bodyReader)
	}

	return &FrameReader{
		source: bodyReader,
	}
}

// Read implements the io.Reader interface.
func (fr *FrameReader) Read(p []byte) (n int, err error) {
	if fr.buffer.Len() > 0 {
		return fr.buffer.Read(p)
	}

	frameHeader := make([]byte, 5)
	if _, err := io.ReadFull(fr.source, frameHeader); err != nil {
		return 0, err
	}

	if frameHeader[0] != grpcDataFrame {
		return 0, io.EOF
	}

	length := binary.BigEndian.Uint32(frameHeader[1:])
	if length == 0 {
		return 0, nil
	}

	// Translate to gRPC message header (1 byte compression flag + 4 bytes length)
	grpcHeader := make([]byte, 5)
	grpcHeader[0] = 0 // No compression
	binary.BigEndian.PutUint32(grpcHeader[1:], length)
	fr.buffer.Write(grpcHeader)

	if _, err := io.CopyN(&fr.buffer, fr.source, int64(length)); err != nil {
		return 0, fmt.Errorf("error copying frame data: %w", err)
	}

	return fr.buffer.Read(p)
}

// StreamingResponseWriter translates a gRPC response stream into a gRPC-Web response stream.
type StreamingResponseWriter struct {
	w                  http.ResponseWriter
	isTextResponse     bool
	headers            http.Header
	trailers           http.Header
	capturedStatusCode int
	headersWritten     bool
	bodyWriter         io.Writer
	flusher            http.Flusher
	frameBuffer        *bytes.Buffer
}

// NewStreamingResponseWriter creates a new gRPC-Web response writer.
func NewStreamingResponseWriter(w http.ResponseWriter, isText bool) *StreamingResponseWriter {
	srw := &StreamingResponseWriter{
		w:                  w,
		isTextResponse:     isText,
		headers:            make(http.Header),
		trailers:           make(http.Header),
		capturedStatusCode: http.StatusOK,
		frameBuffer:        &bytes.Buffer{},
	}

	var writer io.Writer = w
	if isText {
		writer = newFlushingBase64Writer(w)
	}
	srw.bodyWriter = writer

	if flusher, ok := w.(http.Flusher); ok {
		srw.flusher = flusher
	}

	return srw
}

// Header implements http.ResponseWriter.
func (w *StreamingResponseWriter) Header() http.Header {
	return w.headers
}

// WriteHeader implements http.ResponseWriter.
func (w *StreamingResponseWriter) WriteHeader(statusCode int) {
	if w.headersWritten {
		return
	}
	w.capturedStatusCode = statusCode
}

// Write implements http.ResponseWriter.
func (w *StreamingResponseWriter) Write(p []byte) (int, error) {
	if !w.headersWritten {
		w.writeHeaders()
	}

	w.frameBuffer.Write(p)

	for w.frameBuffer.Len() >= 5 {
		grpcHeader := w.frameBuffer.Bytes()[:5]
		length := binary.BigEndian.Uint32(grpcHeader[1:5])

		if uint32(w.frameBuffer.Len()) < 5+length {
			break
		}

		w.frameBuffer.Next(5)

		grpcWebFrameHeader := make([]byte, 5)
		grpcWebFrameHeader[0] = grpcDataFrame
		binary.BigEndian.PutUint32(grpcWebFrameHeader[1:5], length)

		if _, err := w.bodyWriter.Write(grpcWebFrameHeader); err != nil {
			return 0, err
		}

		if _, err := io.CopyN(w.bodyWriter, w.frameBuffer, int64(length)); err != nil {
			return 0, err
		}
	}

	return len(p), nil
}

// Flush implements http.Flusher.
func (w *StreamingResponseWriter) Flush() {
	if !w.headersWritten {
		w.writeHeaders()
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
}

// Finish must be called to complete the gRPC-Web response.
func (w *StreamingResponseWriter) Finish() error {
	if !w.headersWritten {
		w.writeHeaders()
	}

	if w.trailers.Get("Grpc-Status") == "" {
		grpcCode := httpStatusToGrpcCode(w.capturedStatusCode)
		w.trailers.Set("Grpc-Status", strconv.Itoa(int(grpcCode)))
	}
	if w.trailers.Get("Grpc-Message") == "" && w.capturedStatusCode != http.StatusOK {
		w.trailers.Set("Grpc-Message", http.StatusText(w.capturedStatusCode))
	}

	var trailerBuf bytes.Buffer
	if err := w.trailers.Write(&trailerBuf); err != nil {
		return fmt.Errorf("failed to serialize trailers: %w", err)
	}

	frameHeader := make([]byte, 5)
	frameHeader[0] = grpcTrailerFrame
	binary.BigEndian.PutUint32(frameHeader[1:], uint32(trailerBuf.Len()))

	if _, err := w.bodyWriter.Write(frameHeader); err != nil {
		return fmt.Errorf("failed to write trailer frame header: %w", err)
	}
	if _, err := w.bodyWriter.Write(trailerBuf.Bytes()); err != nil {
		return fmt.Errorf("failed to write trailer payload: %w", err)
	}

	if closer, ok := w.bodyWriter.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return fmt.Errorf("failed closing body writer: %w", err)
		}
	}

	w.Flush()
	return nil
}

func (w *StreamingResponseWriter) writeHeaders() {
	if w.headersWritten {
		return
	}
	w.headersWritten = true

	if trailers, ok := w.headers["Trailer"]; ok {
		for _, trailer := range trailers {
			for _, key := range strings.Split(trailer, ",") {
				canonicalKey := http.CanonicalHeaderKey(strings.TrimSpace(key))
				if val, ok := w.headers[canonicalKey]; ok {
					w.trailers[canonicalKey] = val
					delete(w.headers, canonicalKey)
				}
			}
		}
	}
	delete(w.headers, "Trailer")

	for k, v := range w.headers {
		if k != "Content-Type" && k != "Content-Length" {
			w.w.Header()[k] = v
		}
	}

	finalContentType := ContentTypeGRPCWebProto
	if w.isTextResponse {
		finalContentType = ContentTypeGRPCWebTextProto
	}
	w.w.Header().Set("Content-Type", finalContentType)
	w.w.Header().Add("Access-Control-Expose-Headers", "grpc-status, grpc-message")

	w.w.WriteHeader(http.StatusOK)
}

// IsGRPCWebRequest checks if the request is a gRPC-Web request.
func IsGRPCWebRequest(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Content-Type"), ContentTypeGRPCWeb)
}

// IsTextRequest checks if the client accepts a text-encoded response.
func IsTextRequest(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	accept := r.Header.Get("Accept")
	return strings.HasSuffix(contentType, "-text") || strings.Contains(accept, ContentTypeGRPCWebText)
}

func httpStatusToGrpcCode(httpStatusCode int) codes.Code {
	switch httpStatusCode {
	case http.StatusOK:
		return codes.OK
	case http.StatusBadRequest:
		return codes.Internal
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.Unimplemented
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return codes.Unavailable
	default:
		return codes.Unknown
	}
}

// flushingBase64Writer is a base64 encoder that supports http.Flusher.
type flushingBase64Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
	encoder io.WriteCloser
}

func newFlushingBase64Writer(w http.ResponseWriter) *flushingBase64Writer {
	flusher, _ := w.(http.Flusher)
	return &flushingBase64Writer{
		w:       w,
		flusher: flusher,
		encoder: base64.NewEncoder(base64.StdEncoding, w),
	}
}

func (fbw *flushingBase64Writer) Write(p []byte) (int, error) {
	return fbw.encoder.Write(p)
}

func (fbw *flushingBase64Writer) Flush() {
	if fbw.flusher != nil {
		fbw.flusher.Flush()
	}
}

func (fbw *flushingBase64Writer) Close() error {
	return fbw.encoder.Close()
}
