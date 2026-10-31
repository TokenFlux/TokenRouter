package manifest

import "testing"

// validManifest creates a separately owned declaration for each mutation test.
// Parsing exercises the same JSON representation used by real release packages
// instead of bypassing optional-field decoding through Go zero values.
func validManifest(t *testing.T) *Manifest {
	t.Helper()
	m, err := Parse([]byte(`{
 "schemaVersion":1,"id":"tokenflux.checkin","version":"1.2.0","apiVersion":1,"minHostVersion":"0.5.0",
 "name":{"en":"Check-in","zh":"签到"},"description":{"en":"Daily rewards"},"publisher":{"name":"TokenFlux"},"license":"MIT","icon":"icon.svg",
 "permissions":["wallet.credit"],"network":["api.example.com"],
 "backend":{"executable":"bin/plugin","routes":[{"method":"POST","path":"/checkin","auth":"user"}],"tasks":[{"name":"cleanup","schedule":"0 4 * * *"}]},
 "frontend":{"entry":"frontend/index.js","styles":["frontend/index.css"],"pages":[{"id":"home","path":"","audience":"user","title":{"en":"Check-in"}}],"menus":[{"page":"home","label":{"en":"Check-in"},"icon":"frontend/menu.svg"}]},
 "config":{"fields":[{"key":"reward","type":"number","label":{"en":"Reward"},"required":true,"default":0.1,"min":0,"max":100}]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	return m
}
