package resourcebudget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"reasonix/internal/filelock"
	"reasonix/internal/fileutil"
)

// Resources are reservations, not measured usage or OS-enforced limits.
type Resources struct {
	CPU       int `json:"cpu"`
	MemoryMiB int `json:"memory_mib"`
}

type Config struct {
	Budget Resources `json:"budget"`
	Task   Resources `json:"task"`
}

// Defaults preserve conservative admission: one large task, with half the
// machine left for interactive work. Smaller task reservations enable sharing.
func Defaults() Config {
	cpu := workerCores()
	memory := max(1, physicalMemoryMiB()/2)
	return Config{Budget: Resources{cpu, memory}, Task: Resources{cpu, min(2048, memory)}}
}

func (c Config) validate() error {
	if c.Budget.CPU < 1 || c.Budget.CPU > runtime.NumCPU() || c.Budget.MemoryMiB < 1 || c.Budget.MemoryMiB > physicalMemoryMiB() {
		return fmt.Errorf("resource budget must be positive and fit this host (%d CPU, %d MiB)", runtime.NumCPU(), physicalMemoryMiB())
	}
	return checkRequest(c.Task, c.Budget)
}

func checkRequest(r, budget Resources) error {
	if r.CPU < 1 || r.MemoryMiB < 1 || r.CPU > budget.CPU || r.MemoryMiB > budget.MemoryMiB {
		return fmt.Errorf("task reservation (%d CPU, %d MiB) must be positive and fit budget (%d CPU, %d MiB)", r.CPU, r.MemoryMiB, budget.CPU, budget.MemoryMiB)
	}
	return nil
}

func configPath(dir string) string { return filepath.Join(dir, "budget-v2.json") }

func loadConfig(dir string) (Config, error) {
	c := Defaults()
	data, err := os.ReadFile(configPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	// Unknown fields fail closed so a future policy cannot be silently ignored.
	if err := decode(data, &c); err != nil {
		return c, fmt.Errorf("read %s: %w", configPath(dir), err)
	}
	return c, c.validate()
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(path, append(data, '\n'), 0600)
}

// Configure atomically updates the one shared policy. Existing owners retain
// their reservations; reduced budgets block new admission until they drain.
func Configure(ctx context.Context, c Config) error {
	if err := c.validate(); err != nil {
		return err
	}
	dir, err := directory(ctx)
	if err != nil {
		return err
	}
	unlock, err := filelock.Acquire(ctx, filepath.Join(dir, "queue-v2.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return writeJSON(configPath(dir), c)
}

type requestKey struct{}

// WithRequest specifies a known task's reservation without changing a tool's
// provider-visible schema. Zero fields use the shared policy's task defaults.
func WithRequest(ctx context.Context, r Resources) context.Context {
	return context.WithValue(ctx, requestKey{}, r)
}
