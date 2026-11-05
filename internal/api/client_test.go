package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModelsSendsAuthAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("authorization"); got != "Bearer sk-test" {
			t.Errorf("auth header = %q", got)
		}
		if got := r.Header.Get("user-agent"); got != "tlc/"+ClientVersion {
			t.Errorf("user-agent = %q", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"sigma"}],
			"agents":[{"id":"a1","codename":"sigma","label":"Main","model":"gemini:g","reasoningEffort":"high"}],
			"providers":[{"provider":"gemini","available":true,"reasoningEfforts":["low","high"]}],
			"models":[{"id":"gemini:g","label":"G","provider":"gemini","available":true}]}`)
	}))
	defer srv.Close()

	// the base URL's trailing slash and the key's whitespace are normalized
	models, err := New(srv.URL+"/", " sk-test \n").ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models.Agents) != 1 || models.Agents[0].Codename != "sigma" || *models.Agents[0].ReasoningEffort != "high" {
		t.Fatalf("agents = %+v", models.Agents)
	}
	if len(models.Providers) != 1 || !models.Providers[0].Available || len(models.Providers[0].ReasoningEfforts) != 2 {
		t.Fatalf("providers = %+v", models.Providers)
	}
	if len(models.Models) != 1 || models.Models[0].ID != "gemini:g" {
		t.Fatalf("models = %+v", models.Models)
	}
}

func TestListAgentsReturnsTheAgents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"agents":[{"codename":"a","model":"m:1"},{"codename":"b","model":"m:2"}]}`)
	}))
	defer srv.Close()
	agents, err := New(srv.URL, "sk-test").ListAgents(context.Background())
	if err != nil || len(agents) != 2 || agents[1].Codename != "b" {
		t.Fatalf("agents=%+v err=%v", agents, err)
	}
}

func TestCreateAgentPostsOnlyWhatWasSet(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("content-type"); got != "application/json" {
			t.Errorf("content-type = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"a9","codename":"bug-hunter","label":"Bug hunter","model":"openai:gpt-6","reasoningEffort":"high"}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "sk-test").CreateAgent(context.Background(), CreateAgentInput{
		Codename: "bug-hunter", Model: "openai:gpt-6", ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Codename != "bug-hunter" || got.ID != "a9" {
		t.Fatalf("agent = %+v", got)
	}
	if _, has := body["label"]; has {
		t.Fatalf("an empty label must be omitted, body = %v", body)
	}
	if body["codename"] != "bug-hunter" || body["model"] != "openai:gpt-6" || body["reasoningEffort"] != "high" {
		t.Fatalf("body = %v", body)
	}
}

func TestHealthIgnoresTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			fmt.Fprint(w, "ok, not json")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	if err := New(srv.URL, "sk-test").Health(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAPIErrorShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("content-type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"type":"https://turnlith.dev/errors/unauthorized","title":"Unauthorized","status":401,"detail":"missing or invalid api key"}`)
		case "/v1/agents":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"The model 'nope' does not exist","type":"invalid_request_error","code":"model_not_found"}}`)
		case "/health":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "<html>"+string(make([]byte, 400))+"</html>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := New(srv.URL, "sk-test")
	client.SetRetryDelays()
	_, err := client.ListAgents(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != 401 || apiErr.Message != "missing or invalid api key" {
		t.Fatalf("problem+json: err = %#v", err)
	}

	_, err = client.CreateAgent(context.Background(), CreateAgentInput{Model: "nope"})
	apiErr, ok = err.(*APIError)
	if !ok || apiErr.Status != 400 || apiErr.Code != "model_not_found" {
		t.Fatalf("openai shape: err = %#v", err)
	}
	if got := apiErr.Error(); got != "The model 'nope' does not exist (model_not_found, HTTP 400)" {
		t.Fatalf("Error() = %q", got)
	}

	// a non-JSON error body is surfaced, but capped
	err = client.Health(context.Background())
	apiErr, ok = err.(*APIError)
	if !ok || apiErr.Status != 500 || len(apiErr.Message) > 310 {
		t.Fatalf("html body: err = %#v", err)
	}
}

func TestAPIErrorWithoutMessage(t *testing.T) {
	if got := (&APIError{Status: 418}).Error(); got != "server returned HTTP 418" {
		t.Fatalf("Error() = %q", got)
	}
}
