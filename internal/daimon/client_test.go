package daimon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbedRejectsWrongDimensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"vector": make([]float32, vectorDimensions-1),
		})
	}))
	defer server.Close()
	client := &Client{
		embedURL:   server.URL,
		httpClient: server.Client(),
	}
	_, err := client.embed(context.Background(), "test")
	if err == nil || !strings.Contains(err.Error(), "dimensions=383") {
		t.Fatalf("expected dimension error, got %v", err)
	}
}

func TestJSONErrorIncludesRemoteDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"detail":"cold start failed"}`))
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client()}
	err := client.doJSON(
		context.Background(),
		http.MethodPost,
		server.URL,
		map[string]string{"hello": "world"},
		nil,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "cold start failed") {
		t.Fatalf("missing remote detail: %v", err)
	}
}
