package main

import (
	"testing"

	"github.com/labstack/pitot/internal/config"
)

func TestSetupBannerRequired(t *testing.T) {
	scim := config.Config{SCIMToken: "configured"}
	for _, tc := range []struct {
		name  string
		count int64
		cfg   config.Config
		want  bool
	}{
		{"empty without SCIM", 0, config.Config{}, true},
		{"populated without SCIM", 1, config.Config{}, false},
		{"empty with SCIM", 0, scim, false},
		{"populated with SCIM", 1, scim, false},
	} {
		if got := setupBannerRequired(tc.count, tc.cfg); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
}
