// Package router maps tasks to agents based on configurable rules
// with pattern matching, priority ordering, and fallback chains.
package router

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/rohanrgit/ag3nts/internal/agent"
)

// Route defines a single routing rule that maps a task type pattern
// to an agent with an optional fallback.
type Route struct {
	Pattern  string `toml:"pattern"`  // regex matched against task type
	Agent    string `toml:"agent"`    // target agent name
	Fallback string `toml:"fallback"` // fallback agent if primary unavailable
	Priority int    `toml:"priority"` // lower = higher priority

	compiled *regexp.Regexp
}

// Router resolves which agent should handle a given task based on
// configured rules, agent availability, and user overrides.
type Router struct {
	routes  []Route
	primary string          // default agent when no rule matches
	agents  *agent.Registry // used to check availability
}

// New creates a Router with the given routes, primary agent, and registry.
// Routes are compiled and sorted by priority on creation.
func New(routes []Route, primary string, agents *agent.Registry) (*Router, error) {
	sorted := make([]Route, len(routes))
	copy(sorted, routes)

	// Compile regex patterns.
	for i := range sorted {
		re, err := regexp.Compile(sorted[i].Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid route pattern %q: %w", sorted[i].Pattern, err)
		}
		sorted[i].compiled = re
	}

	// Sort by priority (lower number = higher priority).
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	return &Router{
		routes:  sorted,
		primary: primary,
		agents:  agents,
	}, nil
}

// Resolve determines which agent should handle the given task.
//
// Resolution order:
//  1. If agentOverride is non-empty, use that (user explicit override).
//  2. Match taskType against routes in priority order — first match wins.
//  3. If matched agent is unavailable, try the route's fallback.
//  4. If no route matches, use the primary agent.
//  5. If primary is unavailable, return an error.
func (r *Router) Resolve(taskType string, agentOverride string) (string, error) {
	// 1. User override takes precedence.
	if agentOverride != "" {
		if a := r.agents.Get(agentOverride); a != nil {
			return agentOverride, nil
		}
		return "", fmt.Errorf("override agent %q not found in registry", agentOverride)
	}

	// 2. Match against routes.
	for _, route := range r.routes {
		if route.compiled == nil {
			continue
		}
		if !route.compiled.MatchString(taskType) {
			continue
		}

		// Found a matching route — check availability.
		if a := r.agents.Get(route.Agent); a != nil && a.Available() {
			return route.Agent, nil
		}

		// 3. Primary agent unavailable — try fallback.
		if route.Fallback != "" {
			if a := r.agents.Get(route.Fallback); a != nil && a.Available() {
				return route.Fallback, nil
			}
		}

		// Matched route but neither agent nor fallback available — continue to next route.
	}

	// 4. No route matched — use primary.
	if a := r.agents.Get(r.primary); a != nil {
		if a.Available() {
			return r.primary, nil
		}
		return "", fmt.Errorf("primary agent %q is not available", r.primary)
	}

	// 5. Primary not in registry.
	return "", fmt.Errorf("primary agent %q not found in registry", r.primary)
}

// Primary returns the current primary agent name.
func (r *Router) Primary() string {
	return r.primary
}

// SetPrimary changes the default agent used when no routing rule matches.
func (r *Router) SetPrimary(name string) error {
	if r.agents.Get(name) == nil {
		return fmt.Errorf("agent %q not found in registry", name)
	}
	r.primary = name
	return nil
}

// Routes returns a copy of the configured routing rules.
func (r *Router) Routes() []Route {
	out := make([]Route, len(r.routes))
	copy(out, r.routes)
	return out
}
