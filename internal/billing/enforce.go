package billing

import (
	"slices"
	"strings"
	"time"
)

// Decision covers either the total windows of a key's plan (empty Provider)
// or the windows of one provider.
type Decision struct {
	Allowed  bool
	PlanID   string
	PlanName string
	Provider string
	QuotaView
}

// Authorize admits a request against the total windows of the key's plan. The
// provider serving the request is not known yet; AuthorizeProvider checks its
// windows once a credential is selected.
func (s *Store) Authorize(scope string, at time.Time) Decision {
	return s.authorize(scope, "", at)
}

// AuthorizeProvider admits a request to provider against the windows the key's
// plan sets for that provider. Plans without such windows allow it unchanged.
func (s *Store) AuthorizeProvider(scope, provider string, at time.Time) Decision {
	provider = normalizeQuotaProvider(provider)
	if provider == "" {
		return Decision{Allowed: true}
	}
	return s.authorize(scope, provider, at)
}

// LimitsProviders reports whether the key's plan has any provider window, so
// callers can skip resolving the provider of a request otherwise.
func (s *Store) LimitsProviders(scope string) bool {
	scope = strings.TrimSpace(scope)
	limited := false
	s.read(func(state *State) {
		if key := state.Keys[scope]; scope != "" && key != nil && key.PlanID != "" {
			plan, _ := state.FindPlan(key.PlanID)
			limited = slices.ContainsFunc(plan.Windows, func(window QuotaWindow) bool { return window.Provider != "" })
		}
	})
	return limited
}

func (s *Store) authorize(scope, provider string, at time.Time) Decision {
	allowed := Decision{Allowed: true, Provider: provider}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return allowed
	}
	if at.IsZero() {
		at = s.Now()
	}
	if provider != "" {
		// Most requests meet no provider window; skip the state write for them.
		limited := false
		s.read(func(state *State) {
			if key := state.Keys[scope]; key != nil && key.PlanID != "" {
				plan, _ := state.FindPlan(key.PlanID)
				limited = slices.ContainsFunc(plan.Windows, func(window QuotaWindow) bool { return window.Provider == provider })
			}
		})
		if !limited {
			return allowed
		}
	}
	decision := updateResult(s, func(state *State) (Decision, Changes) {
		key := state.Keys[scope]
		if key == nil || key.PlanID == "" {
			return allowed, Changes{}
		}
		plan, _ := state.FindPlan(key.PlanID)
		touched := Changes{Keys: []string{scope}}
		var changed Changes
		if settleExpiredCycles(key, at) {
			changed = touched
		}
		view := providerQuotaView(key, plan, provider, at)
		if !view.Blocked && activateCycles(key, plan, provider, at) {
			changed = touched
			view = providerQuotaView(key, plan, provider, at)
		}
		return Decision{Allowed: !view.Blocked, PlanID: plan.ID, PlanName: plan.Name, Provider: provider, QuotaView: view}, changed
	})
	if decision.Allowed {
		s.blocked.release(scope, provider)
	}
	return decision
}
