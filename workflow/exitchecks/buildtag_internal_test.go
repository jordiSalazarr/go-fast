package exitchecks

import "testing"

func TestBuildsByDefault(t *testing.T) {
	cases := map[string]bool{
		"package x\n":                                    true,
		"//go:build pdfrender\n\npackage x\n":            false,
		"//go:build integration\n\npackage x\n":          false,
		"//go:build !pdfrender\n\npackage x\n":           true,
		"// Copyright\n\n//go:build go1.21\npackage x\n": true,
		"//go:build ((\npackage x\n":                     true,
	}
	for src, want := range cases {
		if got := buildsByDefault([]byte(src)); got != want {
			t.Errorf("buildsByDefault(%q) = %v, want %v", src, got, want)
		}
	}
}
