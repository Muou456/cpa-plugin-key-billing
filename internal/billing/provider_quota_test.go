package billing

import (
	"testing"
	"time"
)

func TestProviderWindowsValidation(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	windows, err := prepareWindows([]QuotaWindow{
		{Name: "Codex", Provider: " CODEX ", RequestLimit: 1, PeriodSeconds: 3600},
		{Name: "Total", RequestLimit: 2, PeriodSeconds: 3600},
		{Name: "xAI", Provider: "xai", RequestLimit: 1, PeriodSeconds: 3600},
	}, nil, now)
	if err != nil || windows[0].Name != "Total" || windows[1].Provider != "codex" || windows[2].Provider != "xai" {
		t.Fatalf("windows = %+v, %v", windows, err)
	}
	plan := Plan{ID: "p", Windows: windows}
	if err := plan.Validate(); err != nil {
		t.Fatalf("one period per provider rejected: %v", err)
	}
	for _, change := range []func(*Plan){
		func(p *Plan) { p.Windows[2].Provider = "codex" },
		func(p *Plan) { p.Windows[1].Provider = "Codex" },
		func(p *Plan) { p.Windows[1].Provider = "co*dex" },
		func(p *Plan) { p.Windows[1].Name = "total" },
	} {
		invalid := clonePlan(plan)
		change(&invalid)
		if invalid.Validate() == nil {
			t.Fatalf("invalid plan accepted: %+v", invalid)
		}
	}
}

func providerQuotaStore(t *testing.T, windows ...QuotaWindow) (*Store, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	store, _ := newAccountStoreWithRepository(t, now)
	store.ReplaceAll(func(state *State) {
		state.Plans = []Plan{{ID: "p", Windows: windows}}
		state.Keys["s"] = &KeyState{PlanID: "p", Preview: "sk-tes…0001"}
	})
	return store, now
}

func providerEvent(at time.Time, provider string) UsageEvent {
	event := subsetEvent("s", at)
	event.Provider = provider
	return event
}

func TestProviderWindowsLimitOnlyTheirProvider(t *testing.T) {
	store, now := providerQuotaStore(t,
		QuotaWindow{ID: "total", Name: "Total", PeriodSeconds: 3600, RequestLimit: 3},
		QuotaWindow{ID: "codex", Name: "Codex", Provider: "codex", PeriodSeconds: 3600, RequestLimit: 1},
		QuotaWindow{ID: "xai", Name: "xAI", Provider: "xai", PeriodSeconds: 3600, RequestLimit: 5},
	)
	if d := store.Authorize("s", now); !d.Allowed || d.Provider != "" || len(d.Windows) != 1 || d.Windows[0].ID != "total" {
		t.Fatalf("total decision = %+v", d)
	}
	var cycles map[string]QuotaCycle
	store.Read(func(state *State) { cycles = state.Keys["s"].Cycles })
	if len(cycles) != 1 {
		t.Fatalf("total admission started provider windows: %+v", cycles)
	}
	if d := store.AuthorizeProvider("s", "Codex", now); !d.Allowed || d.Provider != "codex" || len(d.Windows) != 1 || d.Windows[0].ID != "codex" {
		t.Fatalf("codex decision = %+v", d)
	}
	store.AuthorizeProvider("s", "xai", now)
	store.RecordUsage(providerEvent(now, " CODEX "))

	if d := store.Authorize("s", now); !d.Allowed {
		t.Fatalf("exhausted provider window blocked the total quota: %+v", d)
	}
	if d := store.AuthorizeProvider("s", "codex", now); d.Allowed || !d.Blocked || d.RetryAt.IsZero() {
		t.Fatalf("exhausted codex window allowed: %+v", d)
	}
	if d := store.AuthorizeProvider("s", "xai", now); !d.Allowed || d.Windows[0].Dimensions[0].Used != "0" {
		t.Fatalf("codex usage charged the xai window: %+v", d)
	}
	if d := store.AuthorizeProvider("s", "gemini", now); !d.Allowed || d.PlanID != "" {
		t.Fatalf("provider without windows = %+v", d)
	}
	view, _ := store.KeyViewForScope("s")
	if view.Blocked || len(view.Windows) != 3 || !view.Windows[1].Blocked || view.Windows[1].Provider != "codex" {
		t.Fatalf("key view = %+v", view.QuotaView)
	}

	store.RecordUsage(providerEvent(now, "xai"))
	store.RecordUsage(providerEvent(now, "xai"))
	if d := store.Authorize("s", now); d.Allowed {
		t.Fatalf("exhausted total quota allowed: %+v", d)
	}
}

func TestChangingWindowProviderResetsItsUsage(t *testing.T) {
	store, now := providerQuotaStore(t, QuotaWindow{ID: "w", Name: "Codex", Provider: "codex", PeriodSeconds: 3600, RequestLimit: 1})
	store.AuthorizeProvider("s", "codex", now)
	store.RecordUsage(providerEvent(now, "codex"))
	windows := []QuotaWindow{{ID: "w", Name: "Codex", Provider: "codex", PeriodSeconds: 3600, RequestLimit: 2}}
	if _, err := store.UpdatePlanWithBindings(PlanPatch{ID: "p", Windows: &windows}, nil); err != nil {
		t.Fatal(err)
	}
	if d := store.AuthorizeProvider("s", "codex", now); d.Windows[0].Dimensions[0].Used != "1" {
		t.Fatalf("limit edit reset provider usage: %+v", d)
	}
	windows[0].Provider = "xai"
	if _, err := store.UpdatePlanWithBindings(PlanPatch{ID: "p", Windows: &windows}, nil); err != nil {
		t.Fatal(err)
	}
	if d := store.AuthorizeProvider("s", "xai", now); d.Windows[0].Dimensions[0].Used != "0" {
		t.Fatalf("codex usage moved to xai: %+v", d)
	}
}
