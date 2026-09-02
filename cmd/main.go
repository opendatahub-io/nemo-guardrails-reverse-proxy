package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

const maxRequestBodyBytes = 10 << 20 // 10 MB
const userMappingFile = "/config/user-mapping/config-user-mapping.yaml"

func loadMapping() map[string][]string {
	data, err := os.ReadFile(userMappingFile)
	if err != nil {
		return nil
	}
	var m map[string][]string
	if err := yaml.Unmarshal(data, &m); err != nil {
		log.Printf("error parsing user mapping file: %v", err)
		return nil
	}
	return m
}

// shortSAName extracts the service account name from a fully-qualified
// "system:serviceaccount:<namespace>:<name>" string
func shortSAName(username, prefix string) string {
	if strings.HasPrefix(username, prefix) {
		return strings.TrimPrefix(username, prefix)
	}
	return ""
}

// Lookup a username or shortened serviceaccount name in the inverseMapping
func findConfigFromInverseMapping(inverseMapping map[string]string, username string, saPrefix string) string {
	if configId, ok := inverseMapping[username]; ok {
		return configId
	}

	short := shortSAName(username, saPrefix)
	if configId, ok := inverseMapping[short]; ok {
		return configId
	}

	return ""
}

// invertAndFlattenMapping inverts the config mapping to be a flat dictionary of username -> configID,
// this makes lookup O(1) later
func invertAndFlattenMapping(mapping map[string][]string) map[string]string {
	newMap := make(map[string]string)
	for configID, users := range mapping {
		for _, u := range users {
			if _, exists := newMap[u]; !exists {
				newMap[u] = configID
			}
		}
	}
	return newMap
}

// applyConfigID reads the JSON request body and either sets guardrails.config_id
// to configID (when non-empty) or removes it (when empty). The body and
// Content-Length are updated in place; non-JSON bodies are forwarded unchanged.
func applyConfigID(r *http.Request, configID string) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyBytes))
	if err != nil {
		return err
	}
	_ = r.Body.Close()

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}

	var guardrails map[string]json.RawMessage
	if raw, ok := payload["guardrails"]; ok {
		_ = json.Unmarshal(raw, &guardrails)
	}
	if guardrails == nil {
		guardrails = make(map[string]json.RawMessage)
	}

	if configID != "" {
		encoded, err := json.Marshal(configID)
		if err != nil {
			return err
		}
		guardrails["config_id"] = encoded
	} else {
		delete(guardrails, "config_id")
	}

	guardrailsRaw, err := json.Marshal(guardrails)
	if err != nil {
		return err
	}
	payload["guardrails"] = guardrailsRaw

	newBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewReader(newBody))
	r.ContentLength = int64(len(newBody))
	r.Header.Set("Content-Length", strconv.Itoa(len(newBody)))
	return nil
}

func main() {
	upstreamURL := flag.String("upstream", "http://localhost:8000", "upstream URL to proxy to")
	listenAddr := flag.String("listen-addr", ":8001", "address to listen on")
	namespace := flag.String("namespace", "", "namespace used to resolve short service account names")
	flag.Parse()

	upstream, err := url.Parse(*upstreamURL)
	if err != nil {
		log.Fatalf("invalid upstream URL %q: %v", *upstreamURL, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)

	mapping := loadMapping()
	if mapping == nil {
		log.Printf("warning: no user mapping loaded from %s", userMappingFile)
	}
	inverseMapping := invertAndFlattenMapping(mapping)
	saPrefix := "system:serviceaccount:" + *namespace + ":"

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get("X-Remote-User")
		configID := findConfigFromInverseMapping(inverseMapping, username, saPrefix)
		if err := applyConfigID(r, configID); err != nil {
			log.Printf("error applying config_id: %v", err)
			http.Error(w, "proxy error", http.StatusInternalServerError)
			return
		}
		r.Host = upstream.Host
		proxy.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:         *listenAddr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("user-mapping-proxy listening on %s -> %s", *listenAddr, *upstreamURL)
	log.Fatal(srv.ListenAndServe())
}
