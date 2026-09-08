package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
)

// --- helpers ----------------------------------------------------------------

func makeMapping(pairs ...string) map[string][]string {
	m := make(map[string][]string)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = append(m[pairs[i]], pairs[i+1])
	}
	return m
}

func requestWithBody(t *testing.T, body map[string]any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func bodyConfigID(t *testing.T, r *http.Request) string {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var payload struct {
		Guardrails *struct {
			ConfigID string `json:"config_id"`
		} `json:"guardrails"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload.Guardrails == nil {
		return ""
	}
	return payload.Guardrails.ConfigID
}

// --- shortSAName ------------------------------------------------------------

func TestShortSAName(t *testing.T) {
	tests := []struct {
		username  string
		namespace string
		want      string
	}{
		{"system:serviceaccount:namespace-a:nemo-guardrails-user-alpha", "namespace-a", "nemo-guardrails-user-alpha"},
		{"system:serviceaccount:namespace-b:nemo-guardrails-user-alpha", "namespace-a", ""},
		{"plain-user", "namespace-a", ""},
		{"system:serviceaccount:namespace-a:", "namespace-a", ""},
	}
	for _, tc := range tests {
		got := shortSAName(tc.username, "system:serviceaccount:"+tc.namespace+":")
		if got != tc.want {
			t.Errorf("shortSAName(%q, %q) = %q, want %q", tc.username, tc.namespace, got, tc.want)
		}
	}
}

// --- findConfigFromInverseMapping -------------------------------------------

func TestFindConfig_NilMapping(t *testing.T) {
	if got := findConfigFromInverseMapping(nil, "user-a", "ns"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFindConfig_PlainUsername(t *testing.T) {
	m := invertAndFlattenMapping(makeMapping("config-b", "user-d", "config-a", "user-a", "config-a", "user-b"))
	if got := findConfigFromInverseMapping(m, "user-a", "system:serviceaccount:ns:"); got != "config-a" {
		t.Errorf("got %q, want config-a", got)
	}
	if got := findConfigFromInverseMapping(m, "user-d", "system:serviceaccount:ns:"); got != "config-b" {
		t.Errorf("got %q, want config-b", got)
	}
}

func TestFindConfig_FullyQualifiedSA(t *testing.T) {
	m := invertAndFlattenMapping(makeMapping("config-b", "user-d"))
	username := "system:serviceaccount:namespace-a:user-d"
	if got := findConfigFromInverseMapping(m, username, "system:serviceaccount:namespace-a:"); got != "config-b" {
		t.Errorf("got %q, want config-b", got)
	}
}

func TestFindConfig_ShortSANameMatchesCorrectNamespace(t *testing.T) {
	m := invertAndFlattenMapping(makeMapping("config-a", "sa-alpha"))
	username := "system:serviceaccount:namespace-a:sa-alpha"
	if got := findConfigFromInverseMapping(m, username, "system:serviceaccount:namespace-a:"); got != "config-a" {
		t.Errorf("got %q, want config-a", got)
	}
}

func TestFindConfig_ShortSANameWrongNamespace(t *testing.T) {
	m := invertAndFlattenMapping(makeMapping("config-a", "sa-alpha"))
	username := "system:serviceaccount:namespace-b:sa-alpha"
	if got := findConfigFromInverseMapping(m, username, "system:serviceaccount:namespace-a:"); got != "" {
		t.Errorf("expected no match across namespaces, got %q", got)
	}
}

func TestFindConfig_NoMatch(t *testing.T) {
	m := invertAndFlattenMapping(makeMapping("config-a", "user-a"))
	if got := findConfigFromInverseMapping(m, "unknown-user", "system:serviceaccount:ns:"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- applyConfigID ----------------------------------------------------------

func TestApplyConfigID_InjectsIntoEmptyGuardrails(t *testing.T) {
	r := requestWithBody(t, map[string]any{"model": "model-x"})
	if err := applyConfigID(r, "config-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := bodyConfigID(t, r); got != "config-a" {
		t.Errorf("got %q, want config-a", got)
	}
}

func TestApplyConfigID_OverwritesExistingConfigID(t *testing.T) {
	r := requestWithBody(t, map[string]any{
		"model":      "model-x",
		"guardrails": map[string]any{"config_id": "config-a"},
	})
	if err := applyConfigID(r, "config-b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := bodyConfigID(t, r); got != "config-b" {
		t.Errorf("got %q, want config-b", got)
	}
}

func TestApplyConfigID_RemovesConfigIDWhenEmpty(t *testing.T) {
	r := requestWithBody(t, map[string]any{
		"model":      "model-x",
		"guardrails": map[string]any{"config_id": "config-a"},
	})
	if err := applyConfigID(r, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := bodyConfigID(t, r); got != "" {
		t.Errorf("expected config_id removed, got %q", got)
	}
}

func TestApplyConfigID_PreservesOtherGuardrailsFields(t *testing.T) {
	r := requestWithBody(t, map[string]any{
		"guardrails": map[string]any{
			"config_id": "config-a",
			"input":     map[string]any{"masks": []string{"pii"}},
		},
	})
	if err := applyConfigID(r, "config-b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(r.Body)
	var payload struct {
		Guardrails map[string]json.RawMessage `json:"guardrails"`
	}
	_ = json.Unmarshal(data, &payload)
	if _, ok := payload.Guardrails["input"]; !ok {
		t.Error("other guardrails fields were lost")
	}
}

func TestApplyConfigID_NonJSONBodyPassedThrough(t *testing.T) {
	raw := []byte("not-json")
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	if err := applyConfigID(r, "some-config"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, _ := io.ReadAll(r.Body)
	if string(body) != "not-json" {
		t.Errorf("non-JSON body was modified: %q", body)
	}
}

func TestApplyConfigID_UpdatesContentLength(t *testing.T) {
	r := requestWithBody(t, map[string]any{"model": "qwen36"})
	if err := applyConfigID(r, "weather-config"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, _ := io.ReadAll(r.Body)
	if r.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength %d does not match body length %d", r.ContentLength, len(body))
	}
}

// --- benchmarks -------------------------------------------------------------

func generateMapping(nConfigs, mUsers int) map[string][]string {
	m := make(map[string][]string, nConfigs)
	for i := range nConfigs {
		config := fmt.Sprintf("config-%d", i)
		users := make([]string, mUsers)
		for j := range mUsers {
			users[j] = fmt.Sprintf("user-%d-%d", i, j)
		}
		m[config] = users
	}
	return m
}

func Benchmark(b *testing.B) {
	cases := []struct {
		nConfigs int
		mUsers   int
	}{
		{1, 10},
		{10, 10},
		{100, 10},
		{10, 100},
		{1000, 10_000},
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)

	for _, tc := range cases {
		mapping := generateMapping(tc.nConfigs, tc.mUsers)
		inverseMapping := invertAndFlattenMapping(mapping)
		namespace := "ns"
		username := fmt.Sprintf("user-%d-%d", tc.nConfigs-1, tc.mUsers-1)

		proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			configID := findConfigFromInverseMapping(inverseMapping, r.Header.Get("X-Remote-User"), namespace)
			if err := applyConfigID(r, configID); err != nil {
				http.Error(w, "proxy error", http.StatusInternalServerError)
				return
			}
			r.Host = upstreamURL.Host
			proxy.ServeHTTP(w, r)
		})
		proxyServer := httptest.NewServer(handler)

		shortUsername := username
		fullUsername := fmt.Sprintf("system:serviceaccount:%s:%s", namespace, username)

		b.Run(fmt.Sprintf("%dconfigs_%dusers", tc.nConfigs, tc.mUsers), func(b *testing.B) {
			client := proxyServer.Client()
			body, _ := json.Marshal(map[string]any{"model": "test-model"})
			b.ResetTimer()
			i := 0
			for b.Loop() {
				var user string
				if i%2 == 0 {
					user = shortUsername
				} else {
					user = fullUsername
				}
				i++
				req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/chat/completions", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Remote-User", user)
				resp, err := client.Do(req)
				if err != nil {
					b.Fatalf("request failed: %v", err)
				}
				resp.Body.Close()
			}
		})

		proxyServer.Close()
	}
}
