package exec

import "time"

// MockCommander records calls and returns configured responses.
type MockCommander struct {
	Calls    []MockCall
	Outputs  map[string]MockResult
	Fallback MockResult
}

type MockCall struct {
	Name string
	Args []string
}

type MockResult struct {
	Output string
	Err    error
}

func NewMockCommander() *MockCommander {
	return &MockCommander{
		Outputs: make(map[string]MockResult),
	}
}

func (m *MockCommander) SetOutput(name string, output string, err error) {
	m.Outputs[name] = MockResult{Output: output, Err: err}
}

func (m *MockCommander) record(name string, args ...string) (string, error) {
	m.Calls = append(m.Calls, MockCall{Name: name, Args: args})
	if r, ok := m.Outputs[name]; ok {
		return r.Output, r.Err
	}
	return m.Fallback.Output, m.Fallback.Err
}

func (m *MockCommander) Run(name string, args ...string) (string, error) {
	return m.record(name, args...)
}

func (m *MockCommander) RunWithTimeout(_ time.Duration, name string, args ...string) (string, error) {
	return m.record(name, args...)
}

func (m *MockCommander) RunWithEnv(_ []string, name string, args ...string) (string, error) {
	return m.record(name, args...)
}

func (m *MockCommander) RunWithInput(_ string, name string, args ...string) (string, error) {
	return m.record(name, args...)
}

func (m *MockCommander) Exists(name string) bool {
	m.Calls = append(m.Calls, MockCall{Name: "exists:" + name})
	_, ok := m.Outputs["exists:"+name]
	return ok
}
