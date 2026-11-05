package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// configFixture keeps legacy migration inputs isolated from the process environment.
type configFixture struct {
	test     *testing.T        // owning fixture test
	values   map[string]string // injected environment name values
	document *yaml.Node        // explicit legacy file values
}

// newConfigFixture creates isolated inputs with a valid signing secret.
// Every ordinary load writes a fresh temporary source, while CONFIG_FILE selects
// an explicitly prepared fixture for tests of version and file behavior.
func newConfigFixture(t *testing.T) *configFixture {
	t.Helper()
	return &configFixture{test: t, values: map[string]string{"JWT_SECRET": strings.Repeat("x", 32)}, document: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}}
}

// Setenv records an injected legacy input without mutating process environment state.
func (fixture *configFixture) Setenv(name, value string) { fixture.values[name] = value }

// SetValue records a YAML value for the isolated legacy source document.
func (fixture *configFixture) SetValue(path string, value any) {
	fixture.test.Helper()
	for _, move := range v2Moves {
		if path == move.New || strings.HasPrefix(path, move.New+".") {
			path = move.Old + strings.TrimPrefix(path, move.New)
			break
		}
	}
	require.NoError(fixture.test, setConfigPath(fixture.document.Content[0], path, configValueNode(value)))
}

// Load validates the fixture through the ordinary versioned configuration loader.
func (fixture *configFixture) Load() (*Config, error) { return fixture.load(false) }

// LoadForBootstrap validates the fixture while permitting a missing signing secret.
func (fixture *configFixture) LoadForBootstrap() (*Config, error) { return fixture.load(true) }

// load writes isolated legacy YAML and injects only this fixture's environment.
// An explicit CONFIG_FILE bypasses fixture generation. Repeated ordinary loads
// use new paths so one migration cannot hide a later input change.
func (fixture *configFixture) load(allowMissingJWTSecret bool) (*Config, error) {
	fixture.test.Helper()
	path := fixture.values["CONFIG_FILE"]
	if path == "" && fixture.values["DATA_DIR"] != "" {
		path = filepath.Join(fixture.values["DATA_DIR"], "config.yaml")
	}
	if path == "" {
		path = filepath.Join(fixture.test.TempDir(), "config.yaml")
		raw, err := encodeConfigDocument(fixture.document)
		if err != nil {
			return nil, err
		}
		if err = os.WriteFile(path, raw, 0o600); err != nil {
			return nil, err
		}
	}
	_, err := os.Stat(path)
	return loadFrom(path, err == nil, upgradeEnvironment{LookupEnv: func(name string) (string, bool) { value, ok := fixture.values[name]; return value, ok }}, allowMissingJWTSecret)
}
