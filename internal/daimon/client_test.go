package daimon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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
	)
	if err == nil || !strings.Contains(err.Error(), "cold start failed") {
		t.Fatalf("missing remote detail: %v", err)
	}
}

func TestEmbedAddsAndCachesCloudRunIdentityToken(t *testing.T) {
	token := fakeIdentityToken(time.Now().Add(time.Hour))
	metadataCalls := 0
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataCalls++
		if r.Header.Get("Metadata-Flavor") != "Google" {
			t.Fatal("missing metadata header")
		}
		_, _ = w.Write([]byte(token))
	}))
	defer metadata.Close()
	t.Setenv("GCE_METADATA_URL", metadata.URL)

	inferenceCalls := 0
	inference := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inferenceCalls++
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"vector": make([]float32, vectorDimensions),
		})
	}))
	defer inference.Close()

	client := &Client{
		embedURL:   inference.URL,
		embedToken: newIdentityTokenSource(inference.URL, true),
		httpClient: inference.Client(),
	}
	for range 2 {
		if _, err := client.embed(t.Context(), "hello"); err != nil {
			t.Fatal(err)
		}
	}
	if metadataCalls != 1 || inferenceCalls != 2 {
		t.Fatalf("metadata=%d inference=%d", metadataCalls, inferenceCalls)
	}
}

func fakeIdentityToken(expiry time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"exp":` + strconv.FormatInt(expiry.Unix(), 10) + `}`),
	)
	return header + "." + payload + ".signature"
}
