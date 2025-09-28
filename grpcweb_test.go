package grpcweb_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/soyuka/grpcweb"
)

// TestHandler_EndToEnd performs an integration test of the Handler, simulating a full
// client -> grpcweb-handler -> backend-grpc-server flow.
func TestHandler_EndToEnd(t *testing.T) {
	// 1. Create a fake backend gRPC server.
	// This server expects a native gRPC request and returns a native gRPC response.
	fakeGrpcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assert that the request was correctly translated to native gRPC
		if r.Header.Get("Content-Type") != "application/grpc" {
			t.Errorf("Backend server expected content type 'application/grpc', got '%s'", r.Header.Get("Content-Type"))
			http.Error(w, "invalid content type", http.StatusBadRequest)
			return
		}

		// Read the gRPC-framed request message
		reqBody, err := readGrpcFrame(r.Body)
		if err != nil {
			t.Errorf("Backend server failed to read gRPC request frame: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if string(reqBody) != "hello_world" {
			t.Errorf("Backend server expected 'hello_world', got '%s'", string(reqBody))
		}

		// Send back a gRPC response
		w.Header().Set("Content-Type", "application/grpc")
		// Announce trailers. Go's http.Server/ReverseProxy will promote these to actual trailers.
		w.Header().Add("Trailer", "Grpc-Status")
		w.Header().Add("Trailer", "Grpc-Message")

		// Write a gRPC data frame
		respBody := []byte("hello_back")
		if err := writeGrpcFrame(w, respBody); err != nil {
			t.Fatalf("Backend server failed to write response frame: %v", err)
		}

		// Write trailers as headers. The reverse proxy will handle them correctly.
		w.Header().Set("Grpc-Status", "0")
		w.Header().Set("Grpc-Message", "ok")
	}))
	defer fakeGrpcServer.Close()

	// Parse the backend URL once for the reverse proxy
	backendURL, err := url.Parse(fakeGrpcServer.URL)
	if err != nil {
		t.Fatalf("Failed to parse backend server URL: %v", err)
	}

	// 2. Create our grpcweb Handler, pointing to the fake gRPC server.
	grpcwebHandler := &grpcweb.Handler{
		GRPCServer: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// This simulates the reverse proxy step
			proxy := httputil.NewSingleHostReverseProxy(backendURL)
			proxy.ServeHTTP(w, r)
		}),
	}

	// 3. Define test cases for binary and text modes
	testCases := []struct {
		name                 string
		contentType          string
		isText               bool
		expectedResponseCT   string
		expectedResponseBody string
	}{
		{
			name:                 "Binary",
			contentType:          "application/grpc-web",
			isText:               false,
			expectedResponseCT:   "application/grpc-web+proto",
			expectedResponseBody: "hello_back",
		},
		{
			name:                 "Text",
			contentType:          "application/grpc-web-text",
			isText:               true,
			expectedResponseCT:   "application/grpc-web-text+proto",
			expectedResponseBody: "hello_back",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 4. Create a gRPC-Web client request
			reqBody, err := createGrpcWebRequestBody([]byte("hello_world"), tc.isText)
			if err != nil {
				t.Fatalf("Failed to create request body: %v", err)
			}
			req := httptest.NewRequest("POST", "/service/method", bytes.NewReader(reqBody))
			req.Header.Set("Content-Type", tc.contentType)
			rr := httptest.NewRecorder()

			// 5. Serve the request using our handler
			grpcwebHandler.ServeHTTP(rr, req)

			// 6. Assert the response
			resp := rr.Result()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("Expected status OK; got %d", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); ct != tc.expectedResponseCT {
				t.Errorf("Expected content type '%s'; got '%s'", tc.expectedResponseCT, ct)
			}

			// 7. Parse the gRPC-Web response body
			var responseBodyReader io.Reader
			if tc.isText {
				bodyBytes, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("Failed to read text response body: %v", err)
				}
				decodedBody, err := base64.StdEncoding.DecodeString(string(bodyBytes))
				if err != nil {
					t.Fatalf("Failed to base64-decode text response body: %v. Body was: %q", err, string(bodyBytes))
				}
				responseBodyReader = bytes.NewReader(decodedBody)
			} else {
				responseBodyReader = resp.Body
			}

			// First frame should be data
			frameType, data, err := readGrpcWebFrame(responseBodyReader)
			if err != nil {
				t.Fatalf("Failed to read response data frame: %v", err)
			}
			if frameType != 0x00 {
				t.Errorf("Expected first frame to be DATA (0x00), got %x", frameType)
			}
			if string(data) != tc.expectedResponseBody {
				t.Errorf("Expected response body '%s'; got '%s'", tc.expectedResponseBody, string(data))
			}

			// Second frame should be trailers
			frameType, data, err = readGrpcWebFrame(responseBodyReader)
			if err != nil && err != io.EOF {
				t.Fatalf("Failed to read response trailer frame: %v", err)
			}
			if frameType != 0x80 {
				t.Errorf("Expected second frame to be TRAILER (0x80), got %x", frameType)
			}

			// Normalize and check trailers
			trailers := string(data)
			normalizedTrailers := strings.ToLower(strings.ReplaceAll(trailers, " ", ""))
			if !strings.Contains(normalizedTrailers, "grpc-status:0") {
				t.Errorf("Expected trailers to contain 'grpc-status:0', got '%s'", trailers)
			}
		})
	}
}

// --- Test Helpers ---

// createGrpcWebRequestBody creates a gRPC-Web request body with a single data frame.
func createGrpcWebRequestBody(payload []byte, textEncode bool) ([]byte, error) {
	buf := new(bytes.Buffer)
	// gRPC-Web DATA frame header
	header := []byte{0x00, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(header[1:5], uint32(len(payload)))
	buf.Write(header)
	buf.Write(payload)

	if textEncode {
		encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
		return []byte(encoded), nil
	}
	return buf.Bytes(), nil
}

// readGrpcFrame reads a standard gRPC length-prefixed message.
func readGrpcFrame(r io.Reader) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	// compression := header[0]
	length := binary.BigEndian.Uint32(header[1:5])
	if length == 0 {
		return []byte{}, nil
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// writeGrpcFrame writes a standard gRPC length-prefixed message.
func writeGrpcFrame(w io.Writer, payload []byte) error {
	header := []byte{0x00, 0, 0, 0, 0} // No compression
	binary.BigEndian.PutUint32(header[1:5], uint32(len(payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}

// readGrpcWebFrame reads a gRPC-Web message frame.
func readGrpcWebFrame(r io.Reader) (frameType byte, payload []byte, err error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	frameType = header[0]
	length := binary.BigEndian.Uint32(header[1:5])
	if length == 0 {
		return frameType, nil, nil
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return frameType, payload, nil
}
