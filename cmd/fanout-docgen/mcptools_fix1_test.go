package main

import (
	"strings"
	"testing"
)

func TestMCPReferenceDistinguishesNegotiatedCatalog(t *testing.T) {
	raw, err := renderMCPTools()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Apps-enabled") || !strings.Contains(string(raw), "15 model-visible tools") {
		t.Fatal("Apps catalog described as an ordinary client catalog")
	}
}
