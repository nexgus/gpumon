package shell

import (
	"testing"

	"gpumon/frontend/src/i18n"
	"gpumon/internal/monitor"
	"gpumon/internal/settings"
)

func TestPlaceMainWindow(t *testing.T) {
	primary := screenArea{Work: rect{X: 0, Y: 25, W: 1512, H: 919}, Primary: true}
	left := screenArea{Work: rect{X: -1920, Y: -100, W: 1920, H: 1080}}
	both := []screenArea{left, primary}
	def := settings.Window{Width: settings.DefaultWindowWidth, Height: settings.DefaultWindowHeight}
	saved := func(x, y, w, h int) settings.Window {
		return settings.Window{Width: w, Height: h, HasPosition: true, X: x, Y: y}
	}
	tests := []struct {
		name    string
		saved   settings.Window
		screens []screenArea
		want    rect
		wantOK  bool
	}{
		{"no screen information", saved(10, 40, 900, 600), nil, rect{W: 900, H: 600}, false},
		{"no position yet", def, both, rect{X: 116, Y: 84, W: 1280, H: 800}, true},
		{"inside the primary screen", saved(100, 60, 900, 600), both, rect{X: 100, Y: 60, W: 900, H: 600}, true},
		{"saved width below the minimum", saved(100, 60, 600, 600), both,
			rect{X: 100, Y: 60, W: mainMinWidth, H: 600}, true},
		{"saved width below the minimum, no screen information", saved(10, 40, 300, 600), nil,
			rect{W: mainMinWidth, H: 600}, false},
		{"on the secondary screen", saved(-1500, 0, 1000, 700), both, rect{X: -1500, Y: 0, W: 1000, H: 700}, true},
		{"mostly off the right edge, title bar still reachable",
			saved(1400, 300, 900, 600), both, rect{X: 1400, Y: 300, W: 900, H: 600}, true},
		{"title bar only 50 px visible", saved(1462, 300, 900, 600), both,
			rect{X: 116, Y: 84, W: 1280, H: 800}, true},
		{"title bar above every screen", saved(100, -400, 900, 600), both,
			rect{X: 116, Y: 84, W: 1280, H: 800}, true},
		{"screen no longer attached", saved(-1500, 0, 1000, 700), []screenArea{primary},
			rect{X: 116, Y: 84, W: 1280, H: 800}, true},
		{"far outside", saved(90000, 90000, 900, 600), both, rect{X: 116, Y: 84, W: 1280, H: 800}, true},
		{"saved size larger than the primary screen, no position",
			settings.Window{Width: 3000, Height: 2000}, both, rect{X: 0, Y: 25, W: 3000, H: 2000}, true},
		{"no primary flag uses the first screen", def, []screenArea{{Work: rect{X: 0, Y: 0, W: 1920, H: 1080}}},
			rect{X: 320, Y: 140, W: 1280, H: 800}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := placeMainWindow(tc.saved, tc.screens)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("placeMainWindow = %+v, %v; want %+v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestNeedsQuitConfirm(t *testing.T) {
	tests := []struct {
		active   int
		approved bool
		want     bool
	}{
		{0, false, false},
		{0, true, false},
		{1, false, true},
		{3, false, true},
		{2, true, false},
	}
	for _, tc := range tests {
		if got := needsQuitConfirm(tc.active, tc.approved); got != tc.want {
			t.Errorf("needsQuitConfirm(%d, %v) = %v, want %v", tc.active, tc.approved, got, tc.want)
		}
	}
}

func TestAnyReconnecting(t *testing.T) {
	host := func(st monitor.State) monitor.HostStatus { return monitor.HostStatus{State: st} }
	tests := []struct {
		name  string
		hosts []monitor.HostStatus
		want  bool
	}{
		{"no hosts", nil, false},
		{"connected and disconnected", []monitor.HostStatus{host(monitor.StateConnected), host(monitor.StateDisconnected)}, false},
		{"connecting", []monitor.HostStatus{host(monitor.StateConnecting)}, false},
		{"one reconnecting", []monitor.HostStatus{host(monitor.StateConnected), host(monitor.StateReconnecting)}, true},
	}
	for _, tc := range tests {
		if got := anyReconnecting(tc.hosts); got != tc.want {
			t.Errorf("%s: anyReconnecting = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMenuKeysInBothLanguages(t *testing.T) {
	for _, lang := range []settings.Language{settings.LanguageEnglish, settings.LanguageTraditionalChinese} {
		for _, key := range menuKeys {
			if !i18n.Has(string(lang), key) {
				t.Errorf("%s.json has no %q", lang, key)
			}
		}
	}
	if got := i18n.Text("zh-TW", "menu.about"); got != "關於 GPU Monitor" {
		t.Errorf(`Text("zh-TW", "menu.about") = %q`, got)
	}
	if got := i18n.Text("xx", "tray.quit"); got != "Quit" {
		t.Errorf("unknown language should fall back to English, got %q", got)
	}
}
