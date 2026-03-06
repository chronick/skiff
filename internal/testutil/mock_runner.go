package testutil

import (
	"context"
	"fmt"
	"sync"

	"github.com/chronick/skiff/internal/runner"
)

// Call records a single invocation of the mock runner.
type Call struct {
	Name string
	Args []string
	Opts runner.RunOpts
}

// MockProcessRunner is a test double for runner.ProcessRunner.
type MockProcessRunner struct {
	mu       sync.Mutex
	Calls    []Call
	// Results maps "name arg1 arg2 ..." to (output, error).
	Results  map[string]MockResult
	// DefaultResult is returned when no matching key is found.
	DefaultResult MockResult
}

type MockResult struct {
	Output []byte
	Err    error
}

func NewMockRunner() *MockProcessRunner {
	return &MockProcessRunner{
		Results: make(map[string]MockResult),
	}
}

func (m *MockProcessRunner) Run(ctx context.Context, name string, args []string, opts runner.RunOpts) ([]byte, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, Call{Name: name, Args: args, Opts: opts})
	m.mu.Unlock()

	key := name
	for _, a := range args {
		key += " " + a
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.Results[key]; ok {
		return r.Output, r.Err
	}
	return m.DefaultResult.Output, m.DefaultResult.Err
}

func (m *MockProcessRunner) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

func (m *MockProcessRunner) LastCall() (Call, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Calls) == 0 {
		return Call{}, fmt.Errorf("no calls recorded")
	}
	return m.Calls[len(m.Calls)-1], nil
}

func (m *MockProcessRunner) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
}
