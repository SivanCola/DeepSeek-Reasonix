package billing

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ECB publishes daily euro reference rates for information only — all conversions
// derived from them are estimates.
// https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html
const (
	ECBDailyCSVURL      = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist-90d.csv"
	ECBSourceName       = "ECB euro reference rates"
	DefaultFXMaxAgeDays = 7
	fxCacheFileName     = "ecb-fx-cache.json"
)

// RateTable holds EUR-based cross rates (quote currency units per 1 EUR).
type RateTable struct {
	mu sync.RWMutex
	// rates[date][currency] = units of currency per 1 EUR
	rates     map[string]map[string]float64
	source    string
	fetchedAt time.Time
	stale     bool
}

// fxCacheFile is the on-disk form of RateTable.
type fxCacheFile struct {
	Source    string                        `json:"source"`
	FetchedAt time.Time                     `json:"fetchedAt"`
	Rates     map[string]map[string]float64 `json:"rates"` // date -> currency -> per EUR
}

// NewRateTable returns an empty table.
func NewRateTable() *RateTable {
	return &RateTable{
		rates:  map[string]map[string]float64{},
		source: ECBSourceName,
	}
}

// Clone returns a deep copy safe for concurrent read after clone.
func (t *RateTable) Clone() *RateTable {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := NewRateTable()
	out.source = t.source
	out.fetchedAt = t.fetchedAt
	out.stale = t.stale
	for d, m := range t.rates {
		cp := make(map[string]float64, len(m))
		maps.Copy(cp, m)
		out.rates[d] = cp
	}
	return out
}

// IsEmpty reports whether any observation is present.
func (t *RateTable) IsEmpty() bool {
	if t == nil {
		return true
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.rates) == 0
}

// FetchedAt returns the last successful load/fetch time.
func (t *RateTable) FetchedAt() time.Time {
	if t == nil {
		return time.Time{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.fetchedAt
}

// MarkStale flags the table as older than the acceptance window.
func (t *RateTable) MarkStale(stale bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.stale = stale
	t.mu.Unlock()
}

// IsStale reports the stale flag.
func (t *RateTable) IsStale() bool {
	if t == nil {
		return true
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.stale
}

// SetObservation stores units of currency per 1 EUR for date (YYYY-MM-DD).
func (t *RateTable) SetObservation(date, currency string, perEUR float64) {
	if t == nil || perEUR <= 0 {
		return
	}
	date = strings.TrimSpace(date)
	currency = NormalizeCurrency(currency)
	if date == "" || currency == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rates == nil {
		t.rates = map[string]map[string]float64{}
	}
	if t.rates[date] == nil {
		t.rates[date] = map[string]float64{}
	}
	t.rates[date][currency] = perEUR
	// EUR is always 1 against itself.
	t.rates[date]["EUR"] = 1
}

// Convert converts amount from base to quote using EUR cross rates at or before
// occurred (weekend/holiday falls back to the most recent prior working day).
// ok is false when no usable rate is available within maxAgeDays.
func (t *RateTable) Convert(amount Amount, base, quote string, occurred time.Time, maxAgeDays int) (Amount, *RateSnapshot, bool) {
	base = NormalizeCurrency(base)
	quote = NormalizeCurrency(quote)
	if base == "" || quote == "" {
		return Zero, nil, false
	}
	if base == quote {
		return amount, &RateSnapshot{
			Base: base, Quote: quote, Rate: 1, Source: "identity",
			AsOf: occurred.UTC().Format("2006-01-02"),
		}, true
	}
	if t == nil || t.IsEmpty() {
		return Zero, nil, false
	}
	if maxAgeDays <= 0 {
		maxAgeDays = DefaultFXMaxAgeDays
	}
	date, basePerEUR, quotePerEUR, ok := t.lookupPair(base, quote, occurred)
	if !ok {
		return Zero, &RateSnapshot{Base: base, Quote: quote, Source: ECBSourceName, Stale: true}, false
	}
	// Convert through the two currencies' euro reference rates.
	rate := quotePerEUR / basePerEUR
	asOf, _ := time.Parse("2006-01-02", date)
	ageDays := int(occurred.UTC().Sub(asOf).Hours() / 24)
	if ageDays < 0 {
		ageDays = -ageDays
	}
	// Also consider table fetch age.
	t.mu.RLock()
	fetched := t.fetchedAt
	tableStale := t.stale
	t.mu.RUnlock()
	if !fetched.IsZero() {
		fetchAge := int(time.Since(fetched).Hours() / 24)
		if fetchAge > ageDays {
			ageDays = fetchAge
		}
	}
	stale := tableStale || ageDays > maxAgeDays
	if stale && ageDays > maxAgeDays {
		return Zero, &RateSnapshot{
			Base: base, Quote: quote, Rate: rate, Source: ECBSourceName,
			AsOf: date, Stale: true,
		}, false
	}
	converted := amount.MulRate(rate)
	return converted, &RateSnapshot{
		Base: base, Quote: quote, Rate: rate, Source: ECBSourceName,
		AsOf: date, Stale: stale,
	}, true
}

func (t *RateTable) lookupPair(base, quote string, occurred time.Time) (date string, basePerEUR, quotePerEUR float64, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.rates) == 0 {
		return "", 0, 0, false
	}
	day := occurred.UTC()
	// Walk back up to 14 calendar days for weekends/holidays.
	for i := range 14 {
		d := day.AddDate(0, 0, -i).Format("2006-01-02")
		row, exists := t.rates[d]
		if !exists {
			continue
		}
		bp, bok := perEUR(row, base)
		qp, qok := perEUR(row, quote)
		if bok && qok {
			return d, bp, qp, true
		}
	}
	// Fall back to the latest date not after occurred.
	var best string
	for d := range t.rates {
		if d > day.Format("2006-01-02") {
			continue
		}
		if best == "" || d > best {
			best = d
		}
	}
	if best == "" {
		// Any latest date.
		for d := range t.rates {
			if best == "" || d > best {
				best = d
			}
		}
	}
	if best == "" {
		return "", 0, 0, false
	}
	row := t.rates[best]
	bp, bok := perEUR(row, base)
	qp, qok := perEUR(row, quote)
	if !bok || !qok {
		return best, 0, 0, false
	}
	return best, bp, qp, true
}

func perEUR(row map[string]float64, currency string) (float64, bool) {
	currency = NormalizeCurrency(currency)
	if currency == "EUR" {
		return 1, true
	}
	v, ok := row[currency]
	return v, ok && v > 0
}

// ParseECBHistCSV parses the ECB 90-day (or full) CSV history format.
// Header: Date, USD, JPY, ..., CNY, ...
// Values are units of that currency per 1 EUR.
func ParseECBHistCSV(r io.Reader) (*RateTable, error) {
	sc := bufio.NewScanner(r)
	// Large lines for wide currency lists.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	var headers []string
	table := NewRateTable()
	table.fetchedAt = time.Now().UTC()
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := splitCSVLine(line)
		if lineNo == 1 || (len(headers) == 0 && strings.EqualFold(fields[0], "Date")) {
			headers = fields
			continue
		}
		if len(fields) == 0 || fields[0] == "" {
			continue
		}
		date := fields[0]
		if _, err := time.Parse("2006-01-02", date); err != nil {
			continue
		}
		for i := 1; i < len(fields) && i < len(headers); i++ {
			code := strings.TrimSpace(headers[i])
			val := strings.TrimSpace(fields[i])
			if code == "" || val == "" || val == "N/A" {
				continue
			}
			f, err := strconv.ParseFloat(val, 64)
			if err != nil || f <= 0 {
				continue
			}
			table.SetObservation(date, code, f)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if table.IsEmpty() {
		return nil, fmt.Errorf("billing: empty ECB CSV")
	}
	return table, nil
}

func splitCSVLine(line string) []string {
	// ECB uses simple comma separation without quoted commas in practice.
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// LoadRateTableFile loads a previously saved cache.
func LoadRateTableFile(path string) (*RateTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("billing: empty fx cache")
	}
	var file fxCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("billing: corrupt fx cache: %w", err)
	}
	if len(file.Rates) == 0 {
		return nil, fmt.Errorf("billing: empty fx cache rates")
	}
	t := NewRateTable()
	t.source = file.Source
	if t.source == "" {
		t.source = ECBSourceName
	}
	t.fetchedAt = file.FetchedAt
	t.rates = file.Rates
	// Ensure EUR identity on every row.
	for d, row := range t.rates {
		if row == nil {
			continue
		}
		row["EUR"] = 1
		t.rates[d] = row
	}
	if !t.fetchedAt.IsZero() {
		age := time.Since(t.fetchedAt)
		if age > time.Duration(DefaultFXMaxAgeDays)*24*time.Hour {
			t.stale = true
		}
	}
	return t, nil
}

// SaveRateTableFile atomically writes the cache.
func SaveRateTableFile(path string, t *RateTable) error {
	if t == nil {
		return fmt.Errorf("billing: nil rate table")
	}
	t.mu.RLock()
	file := fxCacheFile{
		Source:    t.source,
		FetchedAt: t.fetchedAt,
		Rates:     t.rates,
	}
	t.mu.RUnlock()
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DefaultFXCachePath returns $REASONIX_HOME/cache/ecb-fx-cache.json or similar.
func DefaultFXCachePath(stateHome string) string {
	stateHome = strings.TrimSpace(stateHome)
	if stateHome == "" {
		return ""
	}
	return filepath.Join(stateHome, "cache", fxCacheFileName)
}

// FetchECBRates downloads the ECB 90-day CSV. Callers must not invoke this on
// the model-request path; use background refresh only.
func FetchECBRates(client *http.Client) (*RateTable, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Get(ECBDailyCSVURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("billing: ECB status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	table, err := ParseECBHistCSV(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	table.fetchedAt = time.Now().UTC()
	return table, nil
}

// FXCache is a process-wide atomic cache. Model paths only Read().
type FXCache struct {
	mu       sync.RWMutex
	table    *RateTable
	path     string
	client   *http.Client
	now      func() time.Time
	maxAge   time.Duration
	updating bool
}

// NewFXCache constructs a cache bound to path. path may be empty (memory only).
func NewFXCache(path string) *FXCache {
	return &FXCache{
		path:   path,
		client: &http.Client{Timeout: 20 * time.Second},
		now:    time.Now,
		maxAge: time.Duration(DefaultFXMaxAgeDays) * 24 * time.Hour,
	}
}

// Load reads disk cache if present. Never blocks on network.
func (c *FXCache) Load() {
	if c == nil || c.path == "" {
		return
	}
	t, err := LoadRateTableFile(c.path)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.table = t
	c.mu.Unlock()
}

// Read returns a snapshot for quoting. Never fetches network.
func (c *FXCache) Read() *RateTable {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.table
}

// SetForTest installs a table (tests / fixtures).
func (c *FXCache) SetForTest(t *RateTable) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.table = t
	c.mu.Unlock()
}

// MaybeRefresh starts a background refresh when the cache is missing or old.
// Safe to call frequently; at most one refresh runs at a time. Never blocks
// the caller on network I/O.
func (c *FXCache) MaybeRefresh() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.updating {
		c.mu.Unlock()
		return
	}
	need := c.table == nil || c.table.IsEmpty()
	if !need && c.table != nil {
		age := c.now().Sub(c.table.FetchedAt())
		need = age > 24*time.Hour
		if age > c.maxAge {
			c.table.MarkStale(true)
		}
	}
	if !need {
		c.mu.Unlock()
		return
	}
	c.updating = true
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			c.updating = false
			c.mu.Unlock()
		}()
		table, err := FetchECBRates(c.client)
		if err != nil {
			return
		}
		if c.path != "" {
			_ = SaveRateTableFile(c.path, table)
		}
		c.mu.Lock()
		c.table = table
		c.mu.Unlock()
	}()
}

// RefreshSync fetches and stores rates (tests / doctor). Prefer MaybeRefresh
// on interactive paths.
func (c *FXCache) RefreshSync() error {
	if c == nil {
		return fmt.Errorf("billing: nil fx cache")
	}
	table, err := FetchECBRates(c.client)
	if err != nil {
		return err
	}
	if c.path != "" {
		if err := SaveRateTableFile(c.path, table); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.table = table
	c.mu.Unlock()
	return nil
}

// globalFX is the process default cache; hosts should call InitGlobalFX.
var (
	globalFXMu sync.RWMutex
	globalFX   *FXCache
)

// InitGlobalFX installs the process FX cache from state home and loads disk.
func InitGlobalFX(stateHome string) *FXCache {
	path := DefaultFXCachePath(stateHome)
	globalFXMu.RLock()
	existing := globalFX
	globalFXMu.RUnlock()
	if existing != nil && existing.path == path {
		existing.MaybeRefresh()
		return existing
	}
	c := NewFXCache(path)
	c.Load()
	globalFXMu.Lock()
	// Another concurrent boot may have installed the same cache while this
	// caller loaded disk. Reuse it so controller rebuilds cannot fan out
	// duplicate ECB refreshes.
	if globalFX != nil && globalFX.path == path {
		existing = globalFX
		globalFXMu.Unlock()
		existing.MaybeRefresh()
		return existing
	}
	globalFX = c
	globalFXMu.Unlock()
	c.MaybeRefresh()
	return c
}

// GlobalFX returns the process cache (may be nil before InitGlobalFX).
func GlobalFX() *FXCache {
	globalFXMu.RLock()
	defer globalFXMu.RUnlock()
	return globalFX
}

// GlobalRateTable is a non-blocking snapshot for quoting.
func GlobalRateTable() *RateTable {
	c := GlobalFX()
	if c == nil {
		return nil
	}
	return c.Read()
}
