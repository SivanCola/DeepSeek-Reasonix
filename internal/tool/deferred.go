package tool

import (
	"fmt"
	"sort"
	"strings"
)

// Deferred tooling exists to break the tension between "support as many MCP
// servers and skills as the user wants" and "keep the prompt cache warm".
//
// Every tool schema a registry exports lands at the front of the
// provider-visible prefix and is re-sent on every single turn. A host with
// fifteen MCP servers pays for a hundred-odd schemas per turn, most of which
// the model never calls, and the context they occupy is context the model
// cannot use for the actual task.
//
// The deferred tier holds those tools back. They are registered, host-visible,
// and callable, but they stay out of Schemas() until something activates them —
// typically a search tool that matched the user's request against the roster.
// The roster itself is cheap: names and one-line descriptions, not schemas.
//
// A registry with nothing deferred behaves exactly as it did before this tier
// existed, so the CLI and the full desktop are unaffected until they opt in.

// DeferredEntry describes one withheld tool: enough for a search tool to match
// against without spending prefix bytes on its JSON Schema.
type DeferredEntry struct {
	Name        string
	Description string
	// Activated reports whether the tool has already been released into
	// Schemas().
	Activated bool
	// Unavailable carries a host-local reason when the backing capability went
	// away — a disconnected MCP server, say. The entry deliberately stays in
	// the roster; see PinPrefix.
	Unavailable string
}

// AddDeferred registers a tool in the deferred tier: stored, resolvable, and
// callable, but withheld from Schemas() until Activate releases it.
//
// A tool already registered in the core tier stays there. Demoting it would
// shrink the exported list and invalidate the cached prefix, which is the exact
// cost this tier exists to avoid — an MCP reconnect swapping in a fresh handle
// must not silently pull a tool out from under the provider.
func (r *Registry) AddDeferred(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := t.Name()
	_, existed := r.tools[name]
	alreadyCore := existed && !r.deferred[name]
	if !r.addLocked(t) {
		return
	}
	if alreadyCore {
		return
	}
	r.deferred[name] = true
}

// Defer moves already-registered tools into the deferred tier, returning the
// names that moved.
//
// This is a session-assembly operation, and the distinction from AddDeferred
// matters. AddDeferred refuses to demote a core tool because doing so
// mid-session shrinks the exported list and invalidates the cached prefix.
// Defer is the deliberate exception: a host calls it between building a session
// and its first turn, when boot has registered everything into the core tier
// but nothing has been sent to the provider yet, so there is no cached prefix
// to lose. Calling it after a turn has run throws that turn's cache away.
func (r *Registry) Defer(names ...string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	moved := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := r.tools[name]; !ok {
			continue
		}
		if r.deferred[name] {
			continue
		}
		// A core tool is never in r.activated, so the activated tail needs no
		// cleanup here.
		r.deferred[name] = true
		moved = append(moved, name)
	}
	return moved
}

// Activate releases deferred tools into Schemas(), appending them in the order
// given. Unknown, core-tier, and already-activated names are skipped. It
// returns the names that actually moved so a caller can report precisely what
// it surfaced rather than echoing back what was asked for.
func (r *Registry) Activate(names ...string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	moved := make([]string, 0, len(names))
	for _, name := range names {
		if !r.deferred[name] {
			continue
		}
		if _, ok := r.tools[name]; !ok {
			continue
		}
		if r.isActivatedLocked(name) {
			continue
		}
		r.activated = append(r.activated, name)
		moved = append(moved, name)
	}
	return moved
}

// IsDeferred reports whether name is registered in the deferred tier, whether
// or not it has been activated.
func (r *Registry) IsDeferred(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.deferred[name]
}

// ActivatedNames returns the deferred tools released so far, in the order they
// were released — the same order they occupy in the tail of Schemas().
func (r *Registry) ActivatedNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, len(r.activated))
	copy(out, r.activated)
	return out
}

// DeferredRoster lists the deferred tier in stable name order.
func (r *Registry) DeferredRoster() []DeferredEntry {
	type pending struct {
		name        string
		tool        Tool
		activated   bool
		unavailable string
	}

	r.mu.RLock()
	names := make([]string, 0, len(r.deferred))
	for name := range r.deferred {
		names = append(names, name)
	}
	sort.Strings(names)

	snapshot := make([]pending, 0, len(names))
	for _, name := range names {
		t := r.tools[name]
		if t == nil {
			continue
		}
		snapshot = append(snapshot, pending{
			name:        name,
			tool:        t,
			activated:   r.isActivatedLocked(name),
			unavailable: r.unavailable[name],
		})
	}
	r.mu.RUnlock()

	// Description() is called after the lock is released, matching
	// ContractEntries. A lazy MCP placeholder takes its spawn mutex inside tool
	// callbacks while that spawn's swap path needs the registry write lock, so
	// holding a registry lock across any tool callback is the AB-BA deadlock
	// this package has already been bitten by once.
	out := make([]DeferredEntry, 0, len(snapshot))
	for _, p := range snapshot {
		out = append(out, DeferredEntry{
			Name:        p.name,
			Description: p.tool.Description(),
			Activated:   p.activated,
			Unavailable: p.unavailable,
		})
	}
	return out
}

// RenderDeferredRoster renders the roster as a turn-scoped message for the
// host to inject, returning "" when nothing is deferred.
//
// This is the counterpart to keeping the search tool's description static. The
// roster is dynamic — it grows as MCP servers finish connecting — so it must
// never land in the system prompt or a tool schema, where a late connection
// would rewrite the cached prefix. Injected as a message it costs a name and
// one line per tool instead of a full JSON Schema each, which is the point:
// the same capabilities stay reachable while the context they occupy drops by
// roughly an order of magnitude.
func RenderDeferredRoster(entries []DeferredEntry) string {
	pending := make([]DeferredEntry, 0, len(entries))
	for _, e := range entries {
		if e.Activated {
			continue
		}
		pending = append(pending, e)
	}
	if len(pending) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("These tools are available but not loaded. Their parameters are not in your tool list, so call ")
	b.WriteString(SearchToolName)
	b.WriteString(" to load one before using it:\n")
	for _, e := range pending {
		fmt.Fprintf(&b, "- %s — %s", e.Name, e.Description)
		if e.Unavailable != "" {
			fmt.Fprintf(&b, " [unavailable: %s]", e.Unavailable)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// PinPrefix marks every tool under prefix unavailable without unregistering it,
// and returns how many it marked.
//
// It is the cache-safe counterpart to RemovePrefix. Dropping a disconnected MCP
// server's "mcp__<server>__" namespace changes the exported tool list, which
// rewrites the front of the provider-visible prefix and costs a full cache miss
// on the very next turn — triggered by a background event the user never asked
// for, and often for a server that reconnects seconds later. Pinning leaves
// every name, description, and schema byte exactly where it was; a call against
// a pinned tool reports reason instead of executing. Re-registering the tool on
// reconnect clears the mark.
func (r *Registry) PinPrefix(prefix, reason string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	pinned := 0
	for _, name := range r.order {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		r.unavailable[name] = reason
		pinned++
	}
	return pinned
}

// Availability reports the host-local reason a registered tool cannot currently
// run. unavailable is false for tools that are fine, which is every tool until
// something calls PinPrefix.
func (r *Registry) Availability(name string) (reason string, unavailable bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	reason, unavailable = r.unavailable[name]
	return reason, unavailable
}

func (r *Registry) isActivatedLocked(name string) bool {
	for _, n := range r.activated {
		if n == name {
			return true
		}
	}
	return false
}
