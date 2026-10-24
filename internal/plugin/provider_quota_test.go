package plugin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"cpa-key-billing/internal/billing"
)

func interceptAfterCredential(t *testing.T, app *App, requestID, credentialID string, metadata map[string]any) RequestInterceptResponse {
	t.Helper()
	if metadata == nil {
		metadata = flowMetadata()
	}
	metadata[MetadataSelectedAuth] = credentialID
	metadata[MetadataSelectedIndex] = credentialID + "-index"
	raw, err := app.HandleMethod(MethodRequestInterceptAfter, mustMarshal(t, RequestInterceptRequest{
		RequestID: requestID, SourceFormat: "openai", Model: flowModel, RequestedModel: flowModel, Metadata: metadata,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var response RequestInterceptResponse
	decodeResult(t, raw, &response)
	return response
}

func TestProviderQuotaIsCheckedForTheSelectedCredential(t *testing.T) {
	app := newAppWithPrice(t, true)
	if _, err := app.store.SyncKeys([]string{testAPIKey}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.CreatePlanWithBindings(billing.Plan{ID: "p", Windows: []billing.QuotaWindow{
		{Name: "Total", RequestLimit: 5, PeriodSeconds: 3600},
		{Name: "Codex", Provider: "Codex", RequestLimit: 1, PeriodSeconds: 3600},
	}}, []string{flowScope()}); err != nil {
		t.Fatal(err)
	}
	app.observeCandidates([]SchedulerAuthCandidate{
		{ID: "codex-auth", Provider: "codex", Attributes: map[string]string{"source_backend": "file"}},
		{ID: "xai-auth", Provider: "xai", Attributes: map[string]string{"source_backend": "config"}},
	})

	requestID := admit(t, app, "openai", "/v1/chat/completions")
	if response := interceptAfterCredential(t, app, requestID, "codex-auth", nil); response.Terminate {
		t.Fatalf("codex quota refused before use: %+v", response)
	}
	publishUsageRecord(t, app, UsageRecord{APIKey: testAPIKey, Model: flowModel, Provider: "codex", RequestedAt: app.store.Now()})
	completeRequest(t, app, requestID)

	requestID = admit(t, app, "openai", "/v1/chat/completions")
	response := interceptAfterCredential(t, app, requestID, "codex-auth", nil)
	if !response.Terminate || response.StatusCode != http.StatusTooManyRequests || response.ResponseHeaders.Get("Retry-After") == "" ||
		!strings.Contains(string(response.ResponseBody), `for provider \"codex\"`) || !strings.Contains(string(response.ResponseBody), "requests 1 / 1") {
		t.Fatalf("exhausted codex quota allowed: %+v %s", response, response.ResponseBody)
	}
	if response := interceptAfterCredential(t, app, requestID, "xai-auth", nil); response.Terminate {
		t.Fatalf("codex quota refused an xai credential: %+v", response)
	}
	if response := interceptAfterCredential(t, app, requestID, "unknown-auth", nil); response.Terminate {
		t.Fatalf("unknown provider refused: %+v", response)
	}
	helper := flowMetadata()
	helper[MetadataSource] = SourcePluginHostModelCallback
	if response := interceptAfterCredential(t, app, requestID, "codex-auth", helper); response.Terminate {
		t.Fatalf("plugin helper refused: %+v", response)
	}
	completeRequest(t, app, requestID)
}

func TestProviderQuotaResolvesProviderFromHostRuntime(t *testing.T) {
	app := newAppWithPrice(t, true)
	if _, err := app.store.SyncKeys([]string{testAPIKey}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.CreatePlanWithBindings(billing.Plan{ID: "p", Windows: []billing.QuotaWindow{
		{Name: "Total", RequestLimit: 5, PeriodSeconds: 3600},
		{Name: "Codex", Provider: "codex", RequestLimit: 1, PeriodSeconds: 3600},
	}}, []string{flowScope()}); err != nil {
		t.Fatal(err)
	}
	app.SetHostCaller(func(method string, _ any) (json.RawMessage, error) {
		if method != hostAuthGetRuntime {
			t.Fatalf("unexpected host call %s", method)
		}
		return json.RawMessage(`{"auth":{"id":"runtime-codex","auth_index":"runtime-codex-index","provider":"codex","type":"codex"}}`), nil
	})

	requestID := admit(t, app, "openai", "/v1/chat/completions")
	if response := interceptAfterCredential(t, app, requestID, "runtime-codex", nil); response.Terminate {
		t.Fatalf("runtime provider refused before use: %+v", response)
	}
	publishUsageRecord(t, app, UsageRecord{APIKey: testAPIKey, Model: flowModel, Provider: "codex", RequestedAt: app.store.Now()})
	completeRequest(t, app, requestID)

	requestID = admit(t, app, "openai", "/v1/chat/completions")
	if response := interceptAfterCredential(t, app, requestID, "runtime-codex", nil); !response.Terminate || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("runtime provider quota not enforced: %+v %s", response, response.ResponseBody)
	}
	completeRequest(t, app, requestID)
}
