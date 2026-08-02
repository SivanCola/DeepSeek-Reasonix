package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"reasonix/internal/fileutil"
)

// configPersistenceSnapshot is a comparable capture of the persisted semantic
// content of a configuration: strings, arrays, maps, paths and permission
// fields. Private to internal/config so the write pipeline does not expand the
// long-term package API.
//
// The snapshot deliberately captures only values (never raw file content);
// diffs report field paths and value categories only.
type configPersistenceSnapshot struct {
	Fields map[string]any
}

// persistenceEmpty is a shared sentinel for nil/empty collections so a nil
// slice and an empty slice do not register as semantic drift.
type persistenceEmpty struct{ kind string }

// persistenceFieldKind classifies a drifted field for error reporting.
type persistenceFieldKind string

const (
	persistenceFieldString     persistenceFieldKind = "string"
	persistenceFieldArray      persistenceFieldKind = "array"
	persistenceFieldMap        persistenceFieldKind = "map"
	persistenceFieldPath       persistenceFieldKind = "path"
	persistenceFieldPermission persistenceFieldKind = "permission"
	persistenceFieldOther      persistenceFieldKind = "other"
)

// tomlPathFieldNames are leaf key names that carry filesystem paths. They are
// used both to classify persistence drift and by the Windows path escape fixer
// to decide whether an unescaped backslash is safe to repair.
var tomlPathFieldNames = map[string]bool{
	"command":            true,
	"path":               true,
	"workspace_root":     true,
	"workspace":          true,
	"system_prompt_file": true,
	"identity_file":      true,
	"output_style_file":  true,
}

// isTOMLPathField reports whether a TOML leaf key is a known filesystem path
// field. Shared with the path-escape scanner in this package.
func isTOMLPathField(leaf string) bool { return tomlPathFieldNames[leaf] }

// persistenceSnapshot captures the exported, toml-tagged fields of cfg as a
// path-indexed map. Both sides of a comparison must be captured with the same
// function so field normalization applies identically.
func persistenceSnapshot(c *Config) configPersistenceSnapshot {
	snap := configPersistenceSnapshot{Fields: map[string]any{}}
	if c == nil {
		return snap
	}
	collectPersistenceFields(reflect.ValueOf(c).Elem(), "", snap.Fields)
	return snap
}

// persistenceFieldsOf captures a single value as path-indexed persistence
// fields, mirroring how collectPersistenceFields captures config fields.
func persistenceFieldsOf(value any, path string) map[string]any {
	out := map[string]any{}
	if value == nil {
		return out
	}
	collectPersistenceFields(reflect.ValueOf(value), path, out)
	return out
}

func persistenceJoin(base, part string) string {
	return persistenceStructJoin(base, part)
}

func persistenceStructJoin(base, field string) string {
	if base == "" {
		return field
	}
	return base + "." + field
}

// persistenceMapJoin quotes map keys so a literal key "a.b" cannot collide with
// nested map a → b (which becomes base{"a"}{"b"}).
func persistenceMapJoin(base, key string) string {
	return base + "{" + strconv.Quote(key) + "}"
}

func persistenceIndexJoin(base string, index int) string {
	return base + "[" + strconv.Itoa(index) + "]"
}

// persistenceNamedJoin identifies one occurrence of a named array-of-tables
// entry (providers/plugins). occurrence is 0-based in document order.
func persistenceNamedJoin(base, name string, occurrence int) string {
	return base + "[" + strconv.Quote(name) + "][" + strconv.Itoa(occurrence) + "]"
}

// collectPersistenceFields walks v into out using typed path segments:
//   - struct fields: ".name" (bare identifiers)
//   - map keys: {"key"} with Go-quoted keys so "a.b" ≠ nested a→b
//   - array indices: [i]
//   - named array-of-tables (providers/plugins): ["name"][occurrence]
//
// so duplicate names and dotted map keys cannot collide.
func collectPersistenceFields(v reflect.Value, path string, out map[string]any) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			out[path] = nil
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			sf := t.Field(i)
			if sf.PkgPath != "" {
				continue // unexported
			}
			tag := sf.Tag.Get("toml")
			if tag == "-" {
				continue
			}
			name := sf.Name
			if tag != "" {
				if idx := strings.IndexByte(tag, ','); idx >= 0 {
					tag = tag[:idx]
				}
				if tag != "" {
					name = tag
				}
			}
			collectPersistenceFields(v.Field(i), persistenceStructJoin(path, name), out)
		}
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			out[path] = persistenceEmpty{kind: "slice"}
			return
		}
		// Named array-of-tables: name + occurrence so same-name entries stay distinct.
		if keyByName := sliceElementNameKeys(v); keyByName {
			occ := map[string]int{}
			for i := 0; i < v.Len(); i++ {
				elem := v.Index(i)
				name := structNameField(elem)
				n := occ[name]
				occ[name] = n + 1
				collectPersistenceFields(elem, persistenceNamedJoin(path, name, n), out)
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			collectPersistenceFields(v.Index(i), persistenceIndexJoin(path, i), out)
		}
	case reflect.Map:
		if v.Len() == 0 {
			out[path] = persistenceEmpty{kind: "map"}
			return
		}
		iter := v.MapRange()
		for iter.Next() {
			key := fmt.Sprintf("%v", iter.Key().Interface())
			collectPersistenceFields(iter.Value(), persistenceMapJoin(path, key), out)
		}
	case reflect.String:
		out[path] = v.String()
	case reflect.Bool:
		out[path] = v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		out[path] = v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		out[path] = v.Uint()
	case reflect.Float32, reflect.Float64:
		out[path] = v.Float()
	default:
		// Unsupported leaf kinds (e.g. time.Time) are excluded from the
		// semantic comparison.
	}
}

// persistenceDiff describes one drifted field between two snapshots.
type persistenceDiff struct {
	Field string
	Kind  persistenceFieldKind
}

func persistenceValuesEqual(a, b any) bool {
	ae, aEmpty := a.(persistenceEmpty)
	be, bEmpty := b.(persistenceEmpty)
	if aEmpty || bEmpty {
		return aEmpty && bEmpty && ae.kind == be.kind
	}
	return reflect.DeepEqual(a, b)
}

func classifyPersistenceField(path string, value any) persistenceFieldKind {
	leaf := persistencePathLeaf(path)
	switch {
	case strings.Contains(path, "permission") || leaf == "deny" || leaf == "allow" || leaf == "ask":
		return persistenceFieldPermission
	case tomlPathFieldNames[leaf]:
		return persistenceFieldPath
	}
	if _, ok := value.(persistenceEmpty); ok {
		return persistenceFieldArray
	}
	if value == nil {
		return persistenceFieldOther
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Slice:
		return persistenceFieldArray
	case reflect.Map:
		return persistenceFieldMap
	case reflect.String:
		return persistenceFieldString
	default:
		return persistenceFieldOther
	}
}

// Diff reports every field whose persisted value differs between the two
// snapshots. The comparison treats nil and empty collections as equal
// (rendering omits empty values).
func (s configPersistenceSnapshot) Diff(t configPersistenceSnapshot) []persistenceDiff {
	var diffs []persistenceDiff
	for path, want := range s.Fields {
		got, ok := t.Fields[path]
		if !ok || !persistenceValuesEqual(want, got) {
			diffs = append(diffs, persistenceDiff{Field: path, Kind: classifyPersistenceField(path, want)})
		}
	}
	for path, got := range t.Fields {
		if _, ok := s.Fields[path]; !ok {
			diffs = append(diffs, persistenceDiff{Field: path, Kind: classifyPersistenceField(path, got)})
		}
	}
	return diffs
}

func (d persistenceDiff) String() string {
	return fmt.Sprintf("%s (%s)", d.Field, d.Kind)
}

// tomlDeltaFieldMask returns the set of dotted field paths (with [i] indices)
// that the delta TOML document actually writes, decoded from the raw parse so
// the delta's own structure decides what is "set" — never the defaults of a
// fresh Config.
func tomlDeltaFieldMask(delta string) (map[string]bool, error) {
	var raw map[string]any
	if _, err := decodeTOMLBytes([]byte(delta), &raw); err != nil {
		return nil, err
	}
	mask := map[string]bool{}
	walkRawTOMLFields(raw, "", mask)
	return mask, nil
}

func walkRawTOMLFields(v any, path string, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			child := rawMapOrStructChild(path, k)
			out[child] = true
			walkRawTOMLFields(val, child, out)
		}
	case []map[string]any:
		walkRawArrayOfTables(path, x, out)
	case []any:
		if len(x) == 0 {
			out[path] = true
			return
		}
		tables := make([]map[string]any, 0, len(x))
		allMaps := true
		for _, elem := range x {
			m, ok := elem.(map[string]any)
			if !ok {
				allMaps = false
				break
			}
			tables = append(tables, m)
		}
		if allMaps {
			walkRawArrayOfTables(path, tables, out)
			return
		}
		for i, elem := range x {
			child := persistenceIndexJoin(path, i)
			out[child] = true
			walkRawTOMLFields(elem, child, out)
		}
	}
}

func walkRawArrayOfTables(path string, tables []map[string]any, out map[string]bool) {
	if isNamedArrayOfTablesPath(path) {
		occ := map[string]int{}
		for i, elem := range tables {
			name, _ := elem["name"].(string)
			name = strings.TrimSpace(name)
			var child string
			if name != "" {
				n := occ[name]
				occ[name] = n + 1
				child = persistenceNamedJoin(path, name, n)
			} else {
				child = persistenceIndexJoin(path, i)
			}
			out[child] = true
			walkRawTOMLFields(elem, child, out)
		}
		return
	}
	for i, elem := range tables {
		child := persistenceIndexJoin(path, i)
		out[child] = true
		walkRawTOMLFields(elem, child, out)
	}
}

// rawMapOrStructChild chooses struct-field vs map-key encoding. Free-form map
// containers (extra_body, headers, env, prices, tool timeouts, ...) must quote
// keys; ordinary config sections use struct-field dots.
func rawMapOrStructChild(path, key string) string {
	if isFreeFormMapPath(path) {
		return persistenceMapJoin(path, key)
	}
	return persistenceStructJoin(path, key)
}

func isFreeFormMapPath(path string) bool {
	if path == "" {
		return false
	}
	leaf := persistencePathLeaf(path)
	switch leaf {
	case "extra_body", "headers", "env", "prices", "tool_timeout_seconds",
		"models", "subagent_models", "subagent_efforts", "args":
		// "models" as string array is not a map; free-form maps that use models
		// as map are rare. prices/extra_body/headers/env are the critical ones.
		return leaf == "extra_body" || leaf == "headers" || leaf == "env" ||
			leaf == "prices" || leaf == "tool_timeout_seconds" ||
			leaf == "subagent_models" || leaf == "subagent_efforts"
	}
	// Nested inside a free-form map: path already contains {"..."}
	return strings.Contains(path, "{")
}

func isNamedArrayOfTablesPath(path string) bool {
	leaf := persistencePathLeaf(path)
	return leaf == "providers" || leaf == "plugins"
}

// persistencePathLeaf returns the last structural name in a typed path
// (struct field, map key, or named table), ignoring indexes.
func persistencePathLeaf(path string) string {
	if path == "" {
		return ""
	}
	// Strip trailing [n] / ["name"][n] index tails for leaf classification.
	for {
		if i := strings.LastIndexByte(path, '['); i >= 0 && strings.HasSuffix(path, "]") {
			// If this is {"key"} map segment ending... map uses {} not []
			path = path[:i]
			continue
		}
		break
	}
	if i := strings.LastIndex(path, "{"); i >= 0 && strings.HasSuffix(path, "}") {
		// map key segment: extract quoted key's relevance - parent leaf is before {
		path = path[:i]
	}
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// sliceElementNameKeys reports whether every non-zero element of the slice has
// a non-empty toml "name" string field (providers/plugins array-of-tables).
func sliceElementNameKeys(v reflect.Value) bool {
	if v.Len() == 0 {
		return false
	}
	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		for elem.Kind() == reflect.Pointer {
			if elem.IsNil() {
				return false
			}
			elem = elem.Elem()
		}
		if elem.Kind() != reflect.Struct {
			return false
		}
		if strings.TrimSpace(structNameField(elem)) == "" {
			return false
		}
	}
	return true
}

func structNameField(v reflect.Value) string {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("toml")
		if tag == "" {
			continue
		}
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		if tag != "name" {
			continue
		}
		f := v.Field(i)
		if f.Kind() == reflect.String {
			return f.String()
		}
	}
	return ""
}

// configFileStateID returns a stable identifier binding a config file path to
// its current bytes and mode. Prefer readConfigFileForEdit + configStateID when
// the same bytes must also be decoded, so load and bind share one observation.
func configFileStateID(path string) (string, error) {
	resolved, data, mode, exists, err := readConfigFileForEdit(path)
	if err != nil {
		return "", err
	}
	return configStateID(resolved, mode, data, exists), nil
}

// readConfigFileForEdit resolves path, reads raw bytes once, and reports the
// mode used for StateID. A missing file returns exists=false and no error.
func readConfigFileForEdit(logicalPath string) (resolved string, data []byte, mode os.FileMode, exists bool, err error) {
	resolved, err = resolveConfigReadPath(logicalPath)
	if err != nil {
		return "", nil, 0, false, err
	}
	info, statErr := os.Lstat(resolved)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return resolved, nil, 0, false, nil
		}
		return "", nil, 0, false, statErr
	}
	data, err = os.ReadFile(resolved)
	if err != nil {
		return "", nil, 0, false, err
	}
	return resolved, data, info.Mode().Perm(), true, nil
}

// configStateID hashes path + mode + raw bytes into the edit-origin token.
// exists=false yields the create-only sentinel "absent".
func configStateID(path string, mode os.FileMode, data []byte, exists bool) string {
	if !exists {
		return "absent"
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%o\x00", path, mode)
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// publishedConfigStateID is the StateID of a body this process just published
// at path with perm. It is derived from the written bytes, never by re-reading
// the path (which could observe another writer).
func publishedConfigStateID(path string, perm os.FileMode, body []byte) string {
	return configStateID(path, perm.Perm(), body, true)
}

// verifyConfigFileState re-checks that the file still matches the state
// captured at read time. expectedState == "absent" requires the file to still
// be absent (create-only semantics).
func verifyConfigFileState(path, expectedState string) error {
	current, err := configFileStateID(path)
	if err != nil {
		return fmt.Errorf("config %s changed during save (state check failed): %w", path, err)
	}
	if current != expectedState {
		return fmt.Errorf("config %s changed by another process while it was being saved", path)
	}
	return nil
}

// writeConfigOptions configures the validated config write pipeline.
type writeConfigOptions struct {
	// scope is the render scope of body (user, project, full). It drives the
	// round-trip verification.
	scope RenderScope
	// delta, when non-empty, contains the incremental TOML that was merged
	// into the existing file to produce body. Every field the delta writes
	// must decode to the same value in the merged body.
	delta string
	// extraChecks maps field paths to expected values that must decode from
	// the final body (used for fields merged outside the delta, e.g.
	// desktop.provider_access).
	extraChecks map[string]any
	// want is the config the caller intends to persist. When set (full-render
	// paths), the decoded candidate is compared against it after the same
	// persistence normalization, so a renderer that silently drops or mangles
	// a field is caught — not just rendering that fails to round-trip.
	want *Config
	// skipRoundTrip disables the semantic round-trip verification for bodies
	// that are deliberately partial (neither a full render nor a delta), e.g.
	// the surgical permissions.allow upsert.
	skipRoundTrip bool
}

// validateAndWriteConfigResolved is the single validated write pipeline for
// TOML config files. Every persisted config write goes through it:
//
//  1. the candidate TOML is parsed with the production parser;
//  2. for user/full files, the parsed config is rendered again and the two
//     parse results are compared semantically with configPersistenceSnapshot,
//     so a rendering that does not round-trip (escapes, control characters,
//     Windows paths) aborts the write;
//  3. for incremental project merges, every field the delta writes is checked
//     against the merged body;
//  4. the original file state is re-verified so a concurrent writer aborts the
//     save instead of being overwritten;
//  5. the body is published with the existing atomic replace mechanism.
//
// On any validation failure the original file is left untouched.
func validateAndWriteConfigResolved(path, body string, perm os.FileMode, opts writeConfigOptions, expectedState string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("save: empty config path")
	}
	if strings.TrimSpace(body) == "" && opts.scope != RenderScopeProject {
		return "", fmt.Errorf("save config %s: refusing to write an empty configuration", path)
	}

	// 1. Parse the candidate with the production parser.
	decoded, err := decodeConfigBodyForValidation(body)
	if err != nil {
		return "", fmt.Errorf("save config %s: generated TOML does not parse: %w", path, err)
	}

	// 2. Semantic round-trip: rendering the decoded config again must yield a
	// document that decodes to the same persisted values.
	//
	// Project files use the incremental delta model: they only carry fields
	// that differ from built-in defaults, and the merge may intentionally
	// retain or drop fields the delta renderer does not emit (for example an
	// explicit `bash_timeout_seconds = 0`). Re-rendering a decoded project
	// body with the full renderer would therefore fabricate default sections
	// that were never persisted. Project scope is verified by the delta field
	// comparison in step 3 instead; user/full files must round-trip exactly.
	if opts.scope == "" {
		opts.scope = RenderScopeFull
	}
	// User/full files must round-trip through the renderer. Project incremental
	// merges (with a delta) may retain fields the project renderer would not
	// emit, so they skip re-render round-trip and rely on step 3. Brand-new
	// project files (no delta) still project-scope round-trip.
	if !opts.skipRoundTrip {
		switch {
		case opts.scope != RenderScopeProject:
			rerendered, err := renderTOMLForScopeErr(decoded, opts.scope)
			if err != nil {
				return "", fmt.Errorf("save config %s: generated TOML cannot be rendered back: %w", path, err)
			}
			if err := validateRenderedRoundTrip(path, body, rerendered); err != nil {
				return "", err
			}
			// Full-render intent: decoded candidate must match the intended config.
			if opts.want != nil && opts.delta == "" {
				wantSnap := persistenceSnapshot(normalizePersistedConfig(opts.want))
				readSnap := persistenceSnapshot(normalizePersistedConfig(decoded))
				if diffs := wantSnap.Diff(readSnap); len(diffs) > 0 {
					return "", persistenceDriftError(path, "persisted semantics", diffs)
				}
			}
		case opts.delta == "" && opts.want != nil:
			// New project file (full project render with intent): re-render at
			// project scope and require semantic equality so a dropped custom
			// provider field cannot pass as "parsed". Incremental project merges
			// with an empty delta (surgical removals / extraChecks only) skip this.
			rerendered, err := renderTOMLForScopeErr(decoded, RenderScopeProject)
			if err != nil {
				return "", fmt.Errorf("save config %s: generated TOML cannot be rendered back: %w", path, err)
			}
			if err := validateRenderedRoundTrip(path, body, rerendered); err != nil {
				return "", err
			}
			if opts.want != nil {
				intended, err := renderTOMLForScopeErr(opts.want, RenderScopeProject)
				if err != nil {
					return "", fmt.Errorf("save config %s: intended project render failed: %w", path, err)
				}
				intendedCfg, err := decodeConfigBodyForValidation(intended)
				if err != nil {
					return "", fmt.Errorf("save config %s: intended project render does not parse: %w", path, err)
				}
				// Compare fields the intended project render writes (name-keyed) so a
				// dropped custom provider field cannot hide outside the body mask.
				mask, err := tomlDeltaFieldMask(intended)
				if err != nil {
					return "", fmt.Errorf("save config %s: intended project body does not parse: %w", path, err)
				}
				wantSnap := persistenceSnapshot(intendedCfg)
				readSnap := persistenceSnapshot(decoded)
				var missed []persistenceDiff
				for fp, want := range wantSnap.Fields {
					if !mask[fp] {
						continue
					}
					got, ok := readSnap.Fields[fp]
					if !ok || !persistenceValuesEqual(want, got) {
						missed = append(missed, persistenceDiff{Field: fp, Kind: classifyPersistenceField(fp, want)})
					}
				}
				if len(missed) > 0 {
					return "", persistenceDriftError(path, "project semantics", missed)
				}
			}
		}
	}

	// 3. Incremental delta verification: every field the delta writes must
	// decode to the intended value from the merged body. Provider/plugin tables
	// are compared by stable name so built-in injection cannot shift indexes.
	mergedSnap := persistenceSnapshot(decoded)
	if opts.delta != "" {
		mask, err := tomlDeltaFieldMask(opts.delta)
		if err != nil {
			return "", fmt.Errorf("save config %s: generated delta does not parse: %w", path, err)
		}
		// Decode the delta without injecting built-in providers so the snapshot
		// describes only the explicit tables the delta wrote.
		deltaCfg, err := decodeConfigBodyExplicit(opts.delta)
		if err != nil {
			return "", fmt.Errorf("save config %s: generated delta does not parse: %w", path, err)
		}
		deltaSnap := persistenceSnapshot(deltaCfg)
		var missed []persistenceDiff
		for fp, want := range deltaSnap.Fields {
			if !mask[fp] {
				continue // the delta document does not write this field
			}
			got, ok := mergedSnap.Fields[fp]
			if !ok || !persistenceValuesEqual(want, got) {
				missed = append(missed, persistenceDiff{Field: fp, Kind: classifyPersistenceField(fp, want)})
			}
		}
		if len(missed) > 0 {
			return "", persistenceDriftError(path, "incremental merge", missed)
		}
	}
	// extraChecks always run (including when delta is empty), so surgical
	// fields like desktop.provider_access are never skipped.
	if len(opts.extraChecks) > 0 {
		var missed []persistenceDiff
		for fp, want := range opts.extraChecks {
			for leaf, wantLeaf := range persistenceFieldsOf(want, fp) {
				got, ok := mergedSnap.Fields[leaf]
				if !ok || !persistenceValuesEqual(wantLeaf, got) {
					missed = append(missed, persistenceDiff{Field: leaf, Kind: classifyPersistenceField(leaf, wantLeaf)})
				}
			}
		}
		if len(missed) > 0 {
			return "", persistenceDriftError(path, "extra checks", missed)
		}
	}

	// 4. Re-check the original file state captured at edit-read time.
	if expectedState != "" {
		if err := verifyConfigFileState(path, expectedState); err != nil {
			return "", err
		}
	}

	// 5. Publish: create-only when the origin was absent so a concurrent create
	// cannot be overwritten; otherwise re-verify immediately before replace.
	// The returned StateID is derived from the published body, never by
	// re-reading the path (another writer must not become our new origin).
	bodyBytes := []byte(body)
	if expectedState == "absent" {
		if err := fileutil.AtomicCreateFile(path, bodyBytes, perm); err != nil {
			return "", fmt.Errorf("write %s: %w", path, err)
		}
		return publishedConfigStateID(path, perm, bodyBytes), nil
	}
	// Final verify immediately before replace shrinks the TOCTOU window. A true
	// cross-process CAS is not available for arbitrary paths; callers that need
	// stronger serialization hold LockUserConfigEdits / file locks around the
	// full Load→Save transaction.
	if expectedState != "" {
		if err := verifyConfigFileState(path, expectedState); err != nil {
			return "", err
		}
	}
	if err := fileutil.AtomicWriteFile(path, bodyBytes, perm); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return publishedConfigStateID(path, perm, bodyBytes), nil
}

// validateRenderedRoundTrip confirms that re-decoding the re-rendered body
// produces the same persisted semantics as decoding the candidate body.
func validateRenderedRoundTrip(path, body, rerendered string) error {
	decoded, err := decodeConfigBodyForValidation(body)
	if err != nil {
		return fmt.Errorf("save config %s: generated TOML does not parse: %w", path, err)
	}
	rerenderedCfg, err := decodeConfigBodyForValidation(rerendered)
	if err != nil {
		return fmt.Errorf("save config %s: re-rendered TOML does not parse: %w", path, err)
	}
	diffs := persistenceSnapshot(decoded).Diff(persistenceSnapshot(rerenderedCfg))
	if len(diffs) > 0 {
		return persistenceDriftError(path, "round-trip", diffs)
	}
	return nil
}

func persistenceDriftError(path, stage string, diffs []persistenceDiff) error {
	names := make([]string, 0, len(diffs))
	for _, d := range diffs {
		names = append(names, d.String())
	}
	return fmt.Errorf("save config %s: %s semantic drift in %s; original file preserved", path, stage, strings.Join(names, ", "))
}

// decodeConfigBodyForValidation parses a candidate config body with the
// production parser. Array-of-table decoding overwrites entries from index 0
// with field-level residual merging, so providers decode into a clean slice
// first and the implicit built-in providers are then restored by name —
// matching the load path semantics used by every persistence comparison.
func decodeConfigBodyForValidation(body string) (*Config, error) {
	decoded := Default()
	decoded.Providers = nil
	if _, err := decodeTOMLBytes([]byte(body), decoded); err != nil {
		return nil, err
	}
	decoded.Providers = mergeProvidersWithDefaults(decoded.Providers)
	return decoded, nil
}

// decodeConfigBodyExplicit parses a candidate body without injecting built-in
// providers. Used for delta snapshots so explicit [[providers]] tables keep
// their authored identity before name-keyed comparison against the merged body.
func decodeConfigBodyExplicit(body string) (*Config, error) {
	decoded := Default()
	decoded.Providers = nil
	if _, err := decodeTOMLBytes([]byte(body), decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
